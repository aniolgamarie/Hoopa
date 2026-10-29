# T132 原 accepted socket 见证准备 v1

状态：独立实验包，不接入核心身份路由。接续[有限关联模型](connector-association-model-v1.md)中“取错另一真实 FD 仍可零 fault 错绑”的反例。实现位于 [acceptedwitness](../../../experiments/sourceidentity/acceptedwitness/)，只解决原入口 socket 的获取边界与字段读取；不证明客户端 connector、BPF 覆盖或跨 socket 关联。

## 1. 接受与见证绑定

公开入口为 `Accept(listener *net.TCPListener, generation uint64) (*Conn, error)`。先拒绝 nil listener、零 generation 和不支持的平台，再由包内 AcceptTCP 取得连接；在向调用者发布或交给其他包装器之前读取该原始连接的见证。调用者不能传入裸 FD、任意 net.Conn、猜测的 cookie 或自造 witness 请求认可。

Conn 私有持有原 TCPConn 与不可变快照，实现 net.Conn 的基本方法；不公开可替换的连接字段、File、SyscallConn 或通用 unwrap。`Snapshot()` 返回值副本，空/零 Conn 明确报错；改动返回副本不能改变内部记录。Close 后仍能读取历史快照，但这只表示曾观察到的 socket，不表示它仍存在、仍连接或 FD 编号未复用。包装后的连接应保留对同一个 Conn 的引用，不能把独立快照粘贴到另一连接后当成新证据。

任一捕获步骤失败均返回空结果和错误，并关闭这次新接受的连接；主错误与关闭错误分别保留。调用者提供的 listener 仍归调用者管理，本包不关闭它。这个严格失败策略仅用于实验入口，不改变 Hoopa 基础代理的降级策略。Accept 的等待由调用者设置 listener deadline 或关闭 listener 控制，本包不承诺总时间上限。

## 2. FD 有效窗口

对包内原 TCPConn 调用 SyscallConn，所有 socket 属性、端点及两个 cookie 的读取都放在**同一次 RawConn.Control 回调**中。FD 不保存到回调之外，不使用 File/dup 建立另一份所有权。Go 标准库保证回调期间 FD 有效；所用本地 Go 1.27.3 的 internal/poll.RawControl 通过引用计数保持该窗口。具体工具链、源码摘要与运行证据见验收归档。

这一保证针对正常 Go Close 与受管理 FD 的交互，不是任意操作的互斥锁；不抵御同进程代码绕过 net 包直接 syscall.Close/dup2、unsafe 修改或恶意替换。这也不证明内核 observer 同时看到了该 socket。Control 失败、未进入回调或读取错误不得发布部分见证；不能仅返回第一个读到的 cookie。

## 3. 本轮范围与 ABI

原型仅支持 Linux/amd64；其他平台在接受前返回 unsupported，不把交叉编译当运行支持。使用现有 Go 标准库，无新 module 依赖。

同一有效窗口中核验 AF_INET、SOCK_STREAM、IPPROTO_TCP，getsockname/getpeername 实测两端为 127.0.0.1 且端口有效。端点不由缓存的 LocalAddr/RemoteAddr 或调用者声明代填。远端早退/复位使查询失败时报告 unavailable，不重试并拼接另一个 FD 的结果。

SO_COOKIE=57、SO_NETNS_COOKIE=71 取固定 8 字节无符号值，检查 syscall 返回码、输出长度恰为 8 和数值非零。缓冲区按本机原生 uint64 读取，不经过 int/float64 截断；JSON 使用十进制字符串。这里的 namespace 来自 socket，不是当前 task 的 namespace。[固定 v6.6 UAPI](https://github.com/torvalds/linux/blob/v6.6/include/uapi/asm-generic/socket.h)、[socket 获取实现](https://github.com/torvalds/linux/blob/v6.6/net/core/sock.c)。接口存在与本机支持分别验证，ENOPROTOOPT 不得伪装为值 0。

Snapshot schema 为 `accepted-socket-witness-v1`，包含 generation、socket_cookie、netns_cookie、local_endpoint、remote_endpoint；association 和 identity_capability 固定 unverified。generation 只是调用者预登记上下文，本包不能证明它对应新鲜 BPF 对象或完整 attachment。没有 PID、进程名称、路径、累计字节或延迟字段，不输出 passed CapabilityRecord。值副本可被外部修改，因此外部传回的 JSON/结构体本身没有信任效力。

## 4. 验证层级

| 层级 | 应验证的事实 | 不能替代的证据 |
| --- | --- | --- |
| 假接口 | 一次 Control、读操作均在有效窗口、两值完整后才返回；短长度/零值/大整数/字段不符/外层错误/关闭错误；不可变副本和空 Conn | 内核 ABI 与实际并发 Close |
| 本机临时 loopback | 自己创建/关闭的两个 TCP 连接；同一私有原 socket 复读、两条连接 cookie 区分；普通 Go Close 的窗口；真实不支持必须拒绝 | 独立外部 oracle、BPF callback、跨 socket 唯一关联、跨 namespace 或其他平台 |
| 隔离内核与核心接线 | 新 ABI 实测 namespace，passive cookie 对原 FD，包装/复位/FD 生命周期和关联正反例，路由前一致边界 | 不能由本包单独宣称完成 |

真实 loopback 测试须显式启用，只访问临时 127.0.0.1 端口，不启动 Mihomo/GOST，不加载 BPF、不修改 cgroup/系统代理。不支持 SO_NETNS_COOKIE 时，完整见证正例须标 unverified/skip；若实际验证了严格拒绝，单独记录这个负向结果，不提升为完整见证通过。

未来核心接线位置在受控 `serveEntry` 接受连接后、登记/启动处理 goroutine/进入 mixed.HandleHoopaConn 之前。当前 001–007 补丁保持不变；接线必须另验 net.Conn 可选接口、半关闭、包装链、数据边界及原 FD 对应关系。不能仅把本包的返回结构接到进程规则就宣布 T132 完成。

## 5. 验收与后续

本轮结果见[见证验收](../../../docs/validation/accepted-socket-witness.md)。T132、T137 和正式 G01 仍由原任务门槛决定；旧 0/100 和历史失败不改评。下一项仍是实际范围/连续性与带 namespace 的内核观察，随后与原 FD 见证共同验证关联，最后审查在线读取与核心接线。
