# T132 小规模 connector 校准执行契约 v1

状态：为未来隔离 Linux lab 准备的离线诊断契约，**不是在线身份解析器或能力放行条件**。接续[关联模型](connector-association-model-v1.md)、[原 accepted socket 见证](accepted-socket-witness-v1.md)与[停止协议候选](connector-stop-spike-v1.md)。本契约不代表已经执行真实 verifier、load、attach 或完整双 cookie 正例，不修改原 G01 **0/100**；T132、T137、T136 及正式矩阵 P0/P1 均未由此放行。

实现入口为 [namespaced BPF profile](../../../experiments/sourceidentity/bpf/namespaced_snapshot.bpf.c)、[准备器](../../../experiments/sourceidentity/prepare.py)、[只读对象检查器](../../../experiments/sourceidentity/inspect_object.py)、[observer runner](../../../experiments/sourceidentity/run_observer.py)、[fixture](../../../experiments/sourceidentity/fixture/main.go)和[离线 checker](../../../experiments/sourceidentity/calibration.py)。协议与失败路径分别由 [fixture 测试](../../../experiments/sourceidentity/fixture/main_test.go)、[FD/进程字段测试](../../../experiments/sourceidentity/fixture/platform_linux_amd64_test.go)和[checker 测试](../../../experiments/sourceidentity/test_calibration.py)验证；假接口测试不代替目标内核证据。

## 1. 观察对象及版本边界

`namespaced` 在 gated body 外保持相同准入、停止和扫描顺序，包含下列三个程序与六个 map：

| 程序 | section | 用途 |
| --- | --- | --- |
| `hoopa_connect` | `cgroup/connect4` | connect 发起者及客户端 socket 观察 |
| `hoopa_lifecycle` | `sockops` | active/passive established 与 close 观察 |
| `hoopa_stop` | `tc` | 通过 TEST_RUN 关闭共享 gate；禁止 attach |

| map | key/value 大小 | 本 profile 的记录语义 |
| --- | --- | --- |
| `capture_config` | 4B / 32B | generation、cgroup、netns、入口端口；挂接前初始化并 freeze |
| `connect_records` | 8B / 112B | schema 1，含 connect socket 的实测 namespace |
| `established_records` | 8B / 56B | schema 2；前 48B 字段位置沿用旧布局，offset 48 追加 u64 `socket_netns_cookie` |
| `close_records` | 8B / 40B | schema 1；不新增、也不推断 close namespace |
| `capture_faults` | 4B / 8B | sticky fault 计数；非零拒绝本轮 |
| `capture_gate` | 4B / 40B | 共享锁、closed/active/admitted/completed/poison 状态 |

established 的 namespace 由 sockops 上的 `bpf_get_netns_cookie(ctx)` 实测，必须非零且与配置相等，不能复制配置值或借当前 callback task 的 namespace 补全。见 [lifecycle body](../../../experiments/sourceidentity/bpf/lifecycle_snapshot.bpf.c)。[解码器](../../../experiments/sourceidentity/observations.py)由显式 profile 决定长度和 schema，绝不按长度自动升级。`capture`、`lifecycle`、`gated` 的既有声明和行为保留；旧 48B/schema 1 established 不可当作带 namespace 的新证据。

[native loader](../../../experiments/sourceidentity/native_loader.py)核对实际程序/section、map ABI 及程序引用的真实 map ID；[session](../../../experiments/sourceidentity/loader_session.py)明确传递 profile。namespaced 强制执行 gate 停止：stop → 逆序关闭 link → LOCK 核对 gate/扫描观察 map/再次核对 gate → 关闭对象。控制程序不 attach。停止失败、未排空、漂移或清理错误不能退化为无 gate 成功快照。该边界仍不证明 callback/RCU 全部退出、hook 覆盖或采集完备。

## 2. fixture 的原 socket 与传输协议

fixture 只实现 Linux/amd64，其他平台明确失败；不加载 BPF、不创建或移动 cgroup、不启动外部程序、不写磁盘。所有 socket 均为 `127.0.0.1` 的 IPv4 TCP。没有 `--allow-loopback` 时在任何 socket 操作前失败。两个命令角色分别是 `--role serve` 和 `--role client`；每个 client 必须由外部启动为独立进程。

共同参数为非零 uint64 `--generation`、`--port 1..65535` 和显式 `--allow-loopback`。serve 还要求 `--count 1..16`，拒绝 token 参数；client 还要求 `--token` 为严格 32 位小写十六进制，拒绝 count 参数。

合法传输帧恰为 **32 字节 token + LF，共 33B**。client 写完后对原 TCPConn 执行 `CloseWrite`；server 读满合法帧，再最多读 1B 检查 EOF，随后回写同帧并完整 Close。client 检查回显 token 与 EOF。总读取上限为 34B，额外 1B 仅用来发现多余输入；截断、无 LF、非法 token、多余字节、回显不一致、超时或任一步错误均拒绝。此半关闭约定只属于本 fixture，不证明 acceptedwitness 在任意核心包装链中的半关闭兼容性。

serve 通过现有 [acceptedwitness.Accept](../../../experiments/sourceidentity/acceptedwitness/conn.go)取得原 accepted FD 的完整 Snapshot，在该连接读取的 token 与 Snapshot 一同记录。client 使用带总 deadline 的 tcp4 Dialer，取得原始 `*net.TCPConn`；[客户端 capture](../../../experiments/sourceidentity/fixture/platform_linux_amd64.go)在同一次 RawConn.Control 内核验 AF_INET/SOCK_STREAM/TCP、实测两侧端点和 SO_COOKIE=57、SO_NETNS_COOKIE=71。两个 cookie 均要求 syscall 成功、长度恰为 8B、值非零，以十进制字符串输出；任一捕获错误均关闭连接并拒绝，不以配置补 namespace。

client 另自报 `pid`（JSON 整数）、`start_ticks`（`/proc/self/stat` field 22，十进制字符串）及 `pidns_inode`（`/proc/self/ns/pid` inode，十进制字符串）。stat 文件有界读取并核对 PID，解析允许 comm 中空格和右括号。它们是进程自报坐标，不是初始 PID namespace 或唯一身份的证明；`start_ticks` 不换算为 BPF 的 `leader_start_boottime_ns`。进程名称、二进制名及路径均未认证，同名只用于构造校准场景。

stdout 为 `sourceidentity-fixture-v1` JSONL：

- serve 首先即时输出一次 `event="listening"`，含 generation 字符串和 port 整数；这只说明 listener 已建立。
- connection 共同字段为 `event="connection"`、`role`、token、generation 字符串、`identity_capability="unverified"`。服务端 role **固定为 `serve`**，附 `witness` 完整 Snapshot；客户端 role 为 `client`，附 `socket_cookie`、`netns_cookie`、`local_endpoint`、`remote_endpoint`、`pid`、`start_ticks`、`pidns_inode`。
- serve 最多缓冲 16 条 connection，全部连接协议及连接 Close、listener.Close 均成功后才统一输出；后续连接失败也不得发布前面的 connection。client 同样在自身 Close 成功后输出。

socket 等待采用 10 秒总网络 deadline，每条连接 deadline 为当前时间加 3 秒与总 deadline 的较早者；client 建连也受总 deadline 限制。这不是 stdout 写入、内核 syscall 或整个进程的硬总超时承诺。错误写 stderr、退出码 1；输出写失败可能留下部分 stdout，**外部 lab 必须保留并检查每个进程的退出码和 stderr**。checker 的 JSONL 输入不含这些外部退出证明，不能用一段可解析 stdout 把失败进程当成功。

## 3. 预登记与 checker 输入

预登记必须发生在实验流量之前。registration 为一个 JSON 对象：

| 字段 | 要求 |
| --- | --- |
| `schema` | `sourceidentity-calibration-registration-v1` |
| `generation`、`cgroup`、`netns_cookie` | 正 uint64 的规范十进制字符串，无前导零，不超过 2^64−1 |
| `port` | JSON 整数，1..65535 |
| `tokens` | 1..16 个互不重复的 32 位小写 hex 字符串 |

generation 对应本次新对象；cgroup 对应实际预备目标；netns cookie 应由外部独立实测登记，不能把配置声明伪装为实测。预登记过程、来源和新鲜性本身仍需 lab 留证，文件写了该值并不证明前提成立。

checker CLI 必需 `--registration`、`--observer-input`、`--observer-result`、`--fixtures FILE [FILE ...]`。observer input 使用 runner 的 `input.json`，schema 为 `sourceidentity-observer-input-v1`，profile 必须 namespaced，其 32B config 必须与登记四项相同。result 使用同轮 `result.json`，要求 namespaced、`observed_unverified`、无 errors、已观察 attachment 以及一致的 `closed_and_drained_observed` gate。

fixture 文件保留各进程原始 JSONL；最多 17 个文件、每输入最多 4MiB、合计最多 33 条 fixture 记录。需恰有一次匹配端口的 listening，且其在 CLI 合并记录顺序中早于任一 serve connection；每个预登记 token 恰有一个 client 和一个 serve 记录。将 server JSONL 放在 `--fixtures` 首位可保留其内部 ready→connection 顺序，不代表跨文件得到可信全局时钟。缺失、重复、额外或不一致输入均拒绝，不靠数量相等补配。

候选计算仅读取 `diagnostic_snapshot` 中原始 key/value hex，以 namespaced 解码并核对 fault、connect/active/passive 数量、socket cookie、实测 namespace、规范化端点及普通握手顺序；忽略嵌入的解码缓存。候选入口没有 token、fixture 或 expected-cookie 参数。**先由 raw BPF 得到候选，再以实际传输 token 配对的 client 原 socket 与 server 原 FD witness 后验核对**，独立 oracle 不得补入或修正候选。

checker 还比较 client.pid 与 connect.tgid_initial、pidns_inode 与 active_pidns_inum；这依赖下一节的初始 PID namespace 前提。它只分别保留 proc start ticks 和 BPF start boottime ns，不声称两者已换算核验。JSON 或快照来源、launcher 放置、连续 attachment 和真实内核覆盖均未由离线比较认证。

成功只输出 `status="consistent_unverified"` 并 exit 0，表示本组输入在有限规则下诊断一致。缺失/矛盾输出 `rejected` 并 exit 1；参数错误同样不能作为成功。runtime、association、completeness、identity_capability、scope_isolation、startup_identity、preregistration 仍为 unverified；不能生成 passed 能力记录或把 consistent 提升为能力通过。

## 4. 未来隔离 lab 的执行顺序

以下是执行者需要逐项留证的顺序，不是自动创建环境或开启 BPF 的脚本。任何前提不足应拒绝该轮；旧结果不改评。

1. **离线固定产物。** 固定源码版本、工具链、内核/BTF、libbpf 和对象 SHA256；以 prepare 的 namespaced profile 私有快照四份 C 源并离线编译，再用 inspector 检查同一摘要对象的三程序/六 map。另 build fixture 并固定其摘要。离线编译/解析不等于真实 verifier 通过。
2. **外部预备范围。** 在获授权的隔离 Linux/amd64、初始 PID namespace、同一 socket netns 中，预先建立专用空 cgroup v2 leaf，确认无旧 socket/残留观察对象和外部流量。固定实际 boot、cgroup、namespace、入口端口及范围依据。普通 IPv4 loopback 三次握手之外的 TFO、simultaneous open、NAT/透明改写、TCP_REPAIR、未知 BPF 改写等不在本轮范围。
3. **预登记少量流。** 首轮使用两个独立 token，将 generation、实测 netns cookie、目标 cgroup 和 port 写入 registration，并保留其摘要与登记时序。两个 client 使用同一已固定二进制名称，但分别启动进程、使用不同 token；同名不是身份依据。
4. **显式挂接后放行。** 操作者在已授权 lab 中显式运行 runner，指定 `--profile namespaced`、固定对象/库及 SHA、已存在 cgroup、登记 generation/netns/port、足够观察时长，并显式给出 `--allow-bpf-load`。默认无此参数拒绝加载。等待 `attached_observer` marker 后，才由已授权的外部 launcher 把 server/client 放入目标 cgroup；必须先完成放置，再开始执行 fixture 建立 socket。listener 也必须在该放置之后创建。runner 自身不创建 cgroup、不移动进程，marker 也不能证明外部放置已完成。
5. **执行两个独立 client。** launcher 先启动 serve/count=2，确认唯一 listening 事件后，再分别启动两个 client 进程，传入各自预登记 token。保留 server/client stdout、stderr、退出码、launcher 的实际放置和时序证据。两个 client 均退出 0、serve 全部记录完成且退出 0后，observer 才可进入 stop；需提前设置足够的有限观察窗口，不能让 runner 到期先截断流量。
6. **停止后离线核对。** 保留 runner marker、input/result、错误与完整退出结果，核验整个流量期间 hook 和范围连续有效。待 gate 停止及最终扫描完成，再运行 checker。任何进程、observer 或外部范围证据失败，均不得只凭 checker exit 0接受该轮。

fixture 命令模板仅供上述 launcher 在**已经完成目标 cgroup 放置后**执行；变量值来自同一 registration，`FIXTURE` 是已固定摘要的可执行文件，不是通过名字认证的程序身份：

```sh
"$FIXTURE" --role serve --generation "$GENERATION" --port "$PORT" --count 2 --allow-loopback
"$FIXTURE" --role client --generation "$GENERATION" --port "$PORT" --token "$TOKEN_1" --allow-loopback
"$FIXTURE" --role client --generation "$GENERATION" --port "$PORT" --token "$TOKEN_2" --allow-loopback
```

三个命令由外部编排为 server 和两个独立 client 进程；不是在普通 shell 中依次阻塞执行，也不是一个进程复用两个 token。stdout/stderr 的分别保存及退出码检查由外部 lab 完成。fixture 不提供 cgroup launcher。

全部采集结束后的离线比较模板（仓库根目录执行）：

```sh
python3 -B experiments/sourceidentity/calibration.py \
  --registration "$REGISTRATION" \
  --observer-input "$OBSERVER_INPUT" \
  --observer-result "$OBSERVER_RESULT" \
  --fixtures "$SERVER_JSONL" "$CLIENT_1_JSONL" "$CLIENT_2_JSONL"
```

这条命令不加载 BPF、不访问 socket，也不补齐缺失证据。真实 verifier、helper/库兼容性、原 socket 双 cookie 支持、launcher 放置、完整 hook 覆盖与连续 attach、停止锁语义和资源回收，仍是外部 lab 必须独立满足的条件。小规模一致结果只支持下一轮研究判断，不改变正式身份样本分母、核心接线资格或产品最低平台声明。
