# T132 有限在线准入实验 v1

状态：待实际验收的实验协议，不是生产身份能力。接续[真实两流校准](../../../docs/validation/isolated-linux-lab.md)与[条件关联模型](connector-association-model-v1.md)。不改动旧 profile、旧核验结论或 G01 分母；T132/T137 保持未完成。

## 范围与资源所有权

仅在独立、无外部网络设备的 Linux guest 中显式运行。guest 初始 PID/net/time namespace、IPv4 127.0.0.1 普通 TCP 握手、固定目标端口、全新 BPF 对象和 map；不支持 TFO、TCP_REPAIR、同时打开、原始报文注入、其他 namespace、未知 BPF 改写及现存目标 socket。所有实际使用的内核、BTF、对象、库、脚本与输入另行固定摘要。

本实验的 connect4 和 sockops 两个 link **挂在 guest 的 cgroup2 根**，覆盖其下所有普通客户端 socket；配置中的 source cgroup 则是专用叶目录。两者不能混为一个 ID。根挂接仅为封闭实验建立覆盖边界，不授权在宿主挂接，也不代表已决定产品的权限部署方式。具有管理权限的外部程序恶意拆卸或修改 BPF、任意迁移与 socket/namespace 操作，不属于本实验保证；不能把 point-in-time link 查询称为持续审计。

单个服务进程持有 listener、accepted socket、BPF 对象、两个 link 和 map FD，不向其他进程传递这些 FD。创建/核验/freeze/根挂接全部完成后，服务进程才进入专用 cgroup、创建 listener 并发布 ready。外部只在 ready 后启动客户端。没有独立 observer 或依赖其 PID 的授权路径。

服务端串行执行准入与撤销：关闭准入状态后，才允许主动停止 writer 或关闭 link；不会将资源关闭与新准入并行。已授予的连接保留不可变身份，后续其他流的冲突不改写其归属；有限实验结束会关闭连接。进程 SIGKILL 同时终止此入口进程，不能留下另一个仍可转发的服务端。SIGKILL 后无最终报告应记录证据缺失，不能伪造正常清理。

## 同步发布与拒绝

| 阶段 | 行为 |
| --- | --- |
| C | connect 钩子只针对固定目标，沿用原始 connector 快照。范围、字段读取、重复 cookie、容量或 gate 错误时拒绝目标 connect；其他目标保持原行为。根覆盖使范围外普通 connect 不能跳过检查后进入同一入口。 |
| A | active-established 以自身 cookie 查 C，核验配置与实际 netns；以实际端点 NOEXIST 发布 tuple→client cookie。 |
| P | passive-established 以自身 netns/反向端点查 tuple，再核验 C；NOEXIST 发布 accepted cookie→client cookie 及完整端点。 |
| Q | 服务端自身 accept，直接从仍持有的原 socket 读取 SO_COOKIE、SO_NETNS_COOKIE 和两端点。在任何 recv/send 前，精确查询绑定及 C，并核对同一 generation/netns/端点。 |

tuple 键为 `<QQIIII`：generation、netns、client IPv4、server IPv4、client port、server port，共 32 B。IP 保存 context 网络序的原字节，端口为主机序。值为 8 B client cookie。

binding 键为 8 B accepted cookie；值 `<QQQQIIIIII` 共 56 B：generation、netns、accepted cookie、client cookie、两 IP、两端口、schema=1、reserved=0。两 cookie 分属不同 socket，不能相互代替。C/config/fault/gate 沿用已有 ABI；新 profile 不保留 established/close 观察 map。

两个新增 hash map 均为 1024 项，只插入，不回收、不覆盖、不 LRU。失败产生 sticky fault；本次有限运行不容许 fault 计数回绕。失败 connect 可以仅留 C，不能生成成功绑定。tuple 复用引发冲突后拒绝新准入，不将陈旧条目赋给新连接。

Q 先检查资源所有权、配置和开放的 gate/fault，再读不可变 binding/C，最后再读 fault/gate，严格解码并核对原 socket。任何缺失、冲突、读取错误、停止、配置错配或失联状态均关闭 accepted socket，应用读写次数为零。双读本身不是通用事务：本实验依赖固定普通握手中 A 完成先于最终 ACK、P 完成先于原 accepted FD 交付，以及根覆盖和同一所有者的撤销顺序。相关源码依据与真实运行必须分别核验，不能由“读到了零 fault”反推覆盖。

授权点为服务端完成这些检查并保存本连接结果。以后新发生的其他连接 fault 阻止后续授权，不回写已授权连接。外部强制 detach 与 Q 并发的通用撤销协议未由此解决；生产核心/helper 的 FD 移交、重启接管和长期回收也未实现。

## 可复查的验收

服务端只回显客户端发来的 32 位十六进制 token 加换行；token 必须在授权后才读取，不能参与关联查询。独立客户端自行记录 PID、启动 ticks、socket cookie、netns 和端点；事后按 token 比较服务端已经冻结的绑定，不用客户端真值修补结果。服务端按连接保存接受、授权、首次读写、返回及关闭的顺序和 actual n。

必须保留两类证据：

- 定向测试：错 cookie/端点/generation/netns、缺记录、fault、gate 关闭、link 缺失及清理错误均不得触发应用回调；正确假接口只核验本地调用顺序，不作为内核证明。
- 真实 guest：至少两个独立正常客户端允许并逐条核对；范围外 connect 拒绝；原 socket 已建立后停止 writer/撤销 link 在应用读写前拒绝；入口进程 SIGKILL 后新客户端不能完成回显。预登记每个场景的判据、保留失败及缺失报告，不把 VM exit 0 当场景通过。

真实多流/复用、故意漏 hook、停止交错、FD 转交、启动身份单位/名称一致性和生产核心接线仍按 T132 后续要求验收。新协议是为了实测在线发布与所有权假设，不能跳过原关联模型 A–E 的完整门槛。全部 JSON 中正式 identity_capability 仍为 unverified；未知名称、路径、启动校验或平台能力不补成已支持。
