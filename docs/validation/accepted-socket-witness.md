# T132 原 accepted socket 见证验收

日期：2026-10-23。继续用户“先完成原型准备”，实现[独立 Go 包](../../experiments/sourceidentity/acceptedwitness/)及[见证契约](../../specs/001-hoopa-proxy-manager/contracts/accepted-socket-witness-v1.md)。未加载 BPF、改变 cgroup、启动外部代理核心或运行正式 G01；requirements 只读 16/16，无扩展 hook。

## 实现与验证结果

包自身 AcceptTCP，在向调用者发布连接前、同一次 RawConn.Control 中读取 socket 类型/协议、实际端点、SO_COOKIE 与 SO_NETNS_COOKIE。私有 Conn 绑定原 TCPConn 和快照，不接收任意 FD/自造 witness，不公开 unwrap/RawConn。错误不发布部分结果，关闭本次 accepted socket，保留捕获及关闭两个错误；listener 所有权留给调用者。Snapshot 值副本和 Close 后历史保留不等于身份或存活证明。

| 检查 | 实际结果 | 限制 |
| --- | --- | --- |
| 假接口与清理 race 测试 | 最终 4 个顶层测试、含子测试共 38 个 pass，0 fail/skip，package 1.027s | 读取假接口与零 TCPConn 清理错误；不是内核见证正例 |
| vet / gofmt | exit 0 / 格式差异为空 | 只检查新 Go package |
| 初次真实 loopback | 沙箱禁止 socket，两个测试失败于创建阶段，日志保留 | 不是代码行为或 cookie 能力测试通过 |
| 受控 loopback 的 Control/Close | 自动审批后执行；普通 Go Close 已标关闭时，同一 Control 内 100 次 SO_COOKIE 读取保持相同；测试 pass | 单次本机、仅 SO_COOKIE；不含双 cookie 正例、强制裸 FD 关闭或 BPF 关联 |
| 原 accepted 双 cookie | 本机 5.10.134-17.2.al8.x86_64 对 SO_NETNS_COOKIE(71) 返回 ENOPROTOOPT；完整正例 skip/unverified | 不用 0 代填，不把 package exit 0 说成完整通过 |
| 捕获失败清理 | 上述不支持分支实际断言结果 Conn=nil、客户端见 EOF、caller listener 仍能接受连接 | 已观测的局部负向结果，所在完整正例测试仍为 skip |
| Darwin/Windows amd64 stub | 最终源码两个 CGO_ENABLED=0 离线编译均 exit 0，制品在 private | 未执行，stub 拒绝能力；不是跨平台验收或正式支持 |

Go 候选实际为 `/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go`，输出 go1.27.3 linux/amd64。每条 Go 命令显式 GOTOOLCHAIN=local、GOPROXY=off，TMPDIR/GOTMPDIR/GOCACHE 均位于项目 `private/t132-accepted-witness-preparation/`；没有自动下载工具链或模块，没有把产物写入代码目录。

## 命令、修订与证据

在上述固定环境下执行的包级命令：

```sh
go test -race -count=1 -json -run '^TestFake' ./experiments/sourceidentity/acceptedwitness
go vet ./experiments/sourceidentity/acceptedwitness
HOOPA_WITNESS_LOOPBACK=1 go test -race -count=1 -timeout=25s -json -run '^TestLoopback' ./experiments/sourceidentity/acceptedwitness
```

正式执行使用固定 Go 绝对路径，完整命令与摘要在机器归档（本地原件 `../../experiments/sourceidentity/accepted-witness-validation.json`，不随Git上传）。执行真实 loopback 只开放自建的 127.0.0.1 临时端口，没有外连、BPF 或系统网络配置；沙箱 EPERM 后的自动审批限定此范围。

稳定审查补出清理双错误的测试缺口：提取私有 finishCapture，验证实际零值 TCPConn.Close 的 EINVAL 与预设捕获错误均保留、返回 Conn 为 nil；成功接受后的测试连接立即登记 Cleanup。初版源码/fake/真实结果保存在 `pre-helper/`，最终再次通过 fake race/vet/gofmt。由于 helper 改动影响错误清理，最终仅重跑 TestLoopbackOriginalSocket，仍按预期 ENOPROTOOPT 并 skip；ControlCloseWindow 本体与 kernelReader 未改，保留原有实际通过的准确输入，不重复计数。

初版真实 suite 为 1 pass/1 skip、0 fail；最终受影响单测为 0 pass/1 skip、0 fail，package exit 0。两次 skip 不是新增成功样本，也不并为身份分母。假接口精度覆盖 uint64 最大值及大于 2^53，返回十进制字符串；坏长度、零 cookie、错误 socket/端点、Control 外层错误和未回调均不发布快照。

完整双 cookie 正例尚未运行到两条 socket 的区分/复读分支，因此无法声称该分支已通过。即使将来该测试通过，它复用同一私有 TCPConn 读取，不是独立外部 oracle。当前没有强制 FD 编号复用、跨 namespace、原 FD 对 passive cookie 或真实核心包装链证据。

## 状态与下一步

| 状态 | 事实 |
| --- | --- |
| 已完成 | 五文件独立见证包、接口/字段/清理的定向验证；本机 SO_COOKIE 的普通 Go Close 窗口及真实不支持时的拒绝清理；两个平台 stub 离线编译 |
| 进行中 | T132 范围/attach 连续性、带 netns 的内核观察、双 cookie/原 FD 正反例、名称一致性、在线一致边界和核心接线 |
| 失败待决策 | 原 G01 0/100 及首次 active 异常保持；本机完整见证因 ENOPROTOOPT 未成立，不用当前宿主强行补常量/猜 namespace；代码初次 tests-first 未定义符号和沙箱 socket EPERM 保留 |
| 环境不足未验证 | 双 cookie 正例、BPF/verifier、跨 socket 关联、独立 oracle、真实 FD 复用/转交、核心半关闭/包装兼容和平台运行支持 |

T132 保持未勾选，139 项/21 完成不变；本轮正式身份样本为零，T137/T136、正式 G01–G03 与 P0/P1 未放行。下一步应在受控且实际支持接口的内核核验范围与两个 cookie，再同内核事件关联；不把当前原型直接接入进程规则。

executor 实际 GPT-6.1-sol / medium 负责新包、定向测试和离线编译；scout 实际 GPT-6-luna / high 负责源码/调用链与稳定实现审查；主代理负责接口/生命周期裁决、真实 loopback、文档与最终验收。最多两个子任务，无再派生；原 observer、关联模型、001–007 核心补丁及旧运行 JSON 保留。
