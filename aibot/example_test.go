package aibot_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
	"github.com/gorilla/websocket"
)

// fakeWeCom 启动一个本地 WebSocket 服务端，模拟企业微信：认证成功后推送一条文本消息，
// 并对收到的每条回复返回成功回执，同时把回复的流式内容写入 replies。
func fakeWeCom(replies chan<- string) *httptest.Server {
	upgrader := websocket.Upgrader{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			var frame aibot.WsFrame
			if err := conn.ReadJSON(&frame); err != nil {
				return
			}
			switch frame.Cmd {
			case aibot.WsCmd.SUBSCRIBE:
				_ = conn.WriteJSON(aibot.WsFrame{Headers: frame.Headers})
				_ = conn.WriteJSON(aibot.WsFrame{
					Cmd:     aibot.WsCmd.CALLBACK,
					Headers: aibot.WsFrameHeaders{ReqID: "req-1"},
					Body:    json.RawMessage(`{"msgid":"m1","chattype":"single","from":{"userid":"alice"},"msgtype":"text","text":{"content":"hello"}}`),
				})
			case aibot.WsCmd.RESPONSE:
				var body struct {
					Stream struct {
						Content string `json:"content"`
					} `json:"stream"`
				}
				_ = json.Unmarshal(frame.Body, &body)
				replies <- body.Stream.Content
				_ = conn.WriteJSON(aibot.WsFrame{Headers: frame.Headers})
			}
		}
	}))
}

// 本示例连接本地模拟服务端，演示注册回调、建连、在新协程中流式回复以及断开的完整流程。
func Example() {
	replies := make(chan string, 1)
	srv := fakeWeCom(replies)
	defer srv.Close()

	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  "bot-id",
		Secret: "bot-secret",
		WSURL:  "ws" + strings.TrimPrefix(srv.URL, "http"),
		Logger: aibot.NewLoggerFunc(func(level, format string, v ...interface{}) {}), // 关闭 SDK 日志
	})

	replyErr := make(chan error, 1)
	client.OnMessageText(func(frame *aibot.WsFrame) {
		var msg aibot.TextMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			replyErr <- err
			return
		}
		// 回调运行在读协程上，回复需在新协程中进行，否则读不到回执。
		go func() {
			streamID := "stream_" + frame.Headers.ReqID
			_, err := client.ReplyStream(frame, streamID, msg.From.UserID+" said: "+msg.Text.Content, true, nil, nil)
			replyErr <- err
		}()
	})

	client.Connect()
	defer client.Disconnect()

	select {
	case content := <-replies:
		fmt.Println("server received:", content)
	case <-time.After(5 * time.Second):
		fmt.Println("timeout")
		return
	}
	fmt.Println("reply error:", <-replyErr)
	// Output:
	// server received: alice said: hello
	// reply error: <nil>
}

// 本示例连接真实的企业微信服务，需要有效的 BotID 与 Secret，因此只编译不运行。
func ExampleNewWSClient() {
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  os.Getenv("WECOM_BOT_ID"),
		Secret: os.Getenv("WECOM_BOT_SECRET"),
	})
	client.OnAuthenticated(func() { fmt.Println("authenticated") })
	client.OnError(func(err error) { fmt.Println("aibot error:", err) })
	client.OnEventEnterChat(func(frame *aibot.WsFrame) {
		go func() {
			// 欢迎语需在收到 enter_chat 事件后 5 秒内回复。
			if _, err := client.ReplyWelcome(frame, aibot.CreateWelcomeReplyBody("你好！")); err != nil {
				fmt.Println("welcome failed:", err)
			}
		}()
	})

	client.Connect()
	defer client.Disconnect()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	<-sig
}

// 本示例在文件消息回调中下载并解密文件，再上传并回复同一文件。需要真实连接，只编译不运行。
func ExampleWSClient_UploadMedia() {
	client := aibot.NewWSClient(aibot.WSClientOptions{
		BotID:  os.Getenv("WECOM_BOT_ID"),
		Secret: os.Getenv("WECOM_BOT_SECRET"),
	})
	client.OnMessageFile(func(frame *aibot.WsFrame) {
		var msg aibot.FileMessage
		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
			return
		}
		go func() {
			data, filename, err := client.DownloadFile(msg.File.URL, msg.File.AesKey)
			if err != nil {
				fmt.Println("download failed:", err)
				return
			}
			if filename == "" {
				filename = "file.dat"
			}
			uploaded, err := client.UploadMedia(data, aibot.UploadMediaOptions{
				Type:     aibot.WeComMediaTypeFile,
				Filename: filename,
			})
			if err != nil {
				fmt.Println("upload failed:", err)
				return
			}
			if _, err := client.ReplyMedia(frame, aibot.WeComMediaTypeFile, uploaded.MediaID, nil); err != nil {
				fmt.Println("reply failed:", err)
			}
		}()
	})
	client.Connect()
	defer client.Disconnect()
}

func ExampleParseMessageBody() {
	frame := &aibot.WsFrame{
		Cmd:     aibot.WsCmd.CALLBACK,
		Headers: aibot.WsFrameHeaders{ReqID: "req-1"},
		Body:    json.RawMessage(`{"msgid":"m1","msgtype":"text","from":{"userid":"alice"},"text":{"content":"hi"}}`),
	}
	fmt.Println(aibot.GetMsgType(frame), aibot.GetMsgID(frame), aibot.GetReqID(frame))

	var msg aibot.TextMessage
	if err := aibot.ParseMessageBody(frame, &msg); err != nil {
		fmt.Println("parse failed:", err)
		return
	}
	fmt.Println(msg.From.UserID, msg.Text.Content)
	// Output:
	// text m1 req-1
	// alice hi
}

func ExampleWecomCrypto() {
	crypto, err := aibot.NewWecomCrypto("token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "")
	if err != nil {
		fmt.Println("init failed:", err)
		return
	}
	encrypted, signature, err := crypto.Encrypt(`{"hello":"world"}`, "1700000000", "nonce")
	if err != nil {
		fmt.Println("encrypt failed:", err)
		return
	}

	// 接收方先校验签名，再解密。
	if !crypto.VerifySignature(signature, "1700000000", "nonce", encrypted) {
		fmt.Println("bad signature")
		return
	}
	plain, err := crypto.Decrypt(encrypted)
	if err != nil {
		fmt.Println("decrypt failed:", err)
		return
	}
	fmt.Println(plain)
	// Output: {"hello":"world"}
}
