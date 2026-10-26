// Package aibot 是企业微信智能机器人（AI Bot）的 Go SDK，通过 WebSocket 长连接接收消息与事件回调，并发送流式回复、模板卡片、欢迎语、主动消息和媒体消息。
//
// 入口是 [NewWSClient]：用 [WSClientOptions] 传入 BotID 与 Secret，注册回调（如 [WSClient.OnMessageText]、[WSClient.OnEventEnterChat]、[WSClient.OnError]），再调用 [WSClient.Connect]。Connect 立即返回，建连、认证、心跳与断线重连都在后台协程中完成；退出时调用 [WSClient.Disconnect] 关闭连接并停止自动重连。
//
// # 用法
//
//	import (
//		"fmt"
//		"os"
//		"os/signal"
//
//		"github.com/go-sphere/wecom-aibot-go-sdk/aibot"
//	)
//
//	client := aibot.NewWSClient(aibot.WSClientOptions{
//		BotID:  os.Getenv("WECOM_BOT_ID"),
//		Secret: os.Getenv("WECOM_BOT_SECRET"),
//	})
//	client.OnError(func(err error) { fmt.Println("aibot error:", err) })
//	client.OnMessageText(func(frame *aibot.WsFrame) {
//		var msg aibot.TextMessage
//		if err := aibot.ParseMessageBody(frame, &msg); err != nil {
//			return
//		}
//		streamID := "stream_" + frame.Headers.ReqID
//		if _, err := client.ReplyStream(frame, streamID, "你说的是: "+msg.Text.Content, true, nil, nil); err != nil {
//			fmt.Println("reply failed:", err)
//		}
//	})
//
//	client.Connect() // 异步建连并认证
//	defer client.Disconnect()
//
//	sig := make(chan os.Signal, 1)
//	signal.Notify(sig, os.Interrupt)
//	<-sig
//
// # 回调与并发
//
// 消息与事件回调（OnMessage*、OnEvent*）在当前连接生命周期的独立分发协程上按到达顺序串行执行，回执帧仍由读协程投递，因此可以在回调内直接调用
// [WSClient.Reply]、[WSClient.ReplyStream]、[WSClient.ReplyWelcome] 等方法并等待回执。耗时操作会推迟后续消息的分发，可自行放到新协程中；OnAuthenticated 仍在读协程上同步执行。
// 待分发回调使用可增长的内存队列，积压达到 256 条时记录警告，不因队列满而丢弃消息。
// 若回调处理速度持续低于到达速度，内存占用会增长，应用应将耗时工作放到自行管理的协程中。
// Disconnect 或重试耗尽会丢弃未开始的回调；已开始的回调继续完成，但不能消费下一次 Connect 的队列。
// 服务端推送 disconnected_event 时立即关闭连接，依次分发已排队的回调和该事件，再调用 OnDisconnected 并退出分发协程。
// 回调可以在 Connect 前后随时注册或替换，注册方法可并发调用。
//
// # 回复与发送
//
// 被动回复（Reply 系列）必须使用收到的回调帧，同一 req_id 的回复按调用顺序串行发送，每条都阻塞等待服务端回执（最长 5 秒）。流式回复用同一个 streamID 多次调用
// [WSClient.ReplyStream]，最后一次传 finish=true；高频中间帧可用
// [WSClient.ReplyStreamNonBlocking] 跳过仍在等待回执的发送。主动推送使用
// [WSClient.SendMessage]、[WSClient.SendMarkdown]、[WSClient.SendTemplateCard]；媒体先用 [WSClient.UploadMedia] 上传，再用 [WSClient.ReplyMedia] 或
// [WSClient.SendMediaMessage] 发送。图片、文件、视频消息中的资源用
// [WSClient.DownloadFile] 下载并解密。
//
// [WecomCrypto] 提供企业微信回调消息的签名校验与加解密，不依赖 WSClient。
package aibot
