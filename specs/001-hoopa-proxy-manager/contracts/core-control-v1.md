# 受保护核心控制 v1（T026 基础模式）

日期：2026-10-27。状态：受限基础模式已按[T026有限验收](../../../docs/validation/p1-basic-control.md)实现并验证。该版本只授予 Linux/amd64、
无 BPF、无计量的受限 basic 配置能力，不授予原始进程身份能力或任意 Mihomo 配置支持。
旧核心及旧控制 schema 1/2 保留历史含义，不能冒充本版本。

## 启动与身份

产品显式设置 `HOOPA_CONTROL_PROTOCOL=1`、`HOOPA_RUN_ID`、既有
`HOOPA_GUARD_GENERATION` / `HOOPA_GUARD_CONFIG_SHA256` 和 `HOOPA_RUN_NONCE`。
protocol、run ID 或 run nonce 变量出现即进入受保护路径；空值、未知版本或缺项拒绝，不能落入
stock 启动。run ID 为独立 ASCII ID；nonce 为 64 个小写十六进制字符，不在响应返回。
basic v1 拒绝任何 identity handoff 和非空 tracing 环境；仍使用固定 profile 的全部
配置限制、私有文件检查与独立应用凭据。配置原始字节的 SHA-256 不包含每次 RunID。

所有应用 socket 先 bind、保持未 listen；控制 socket 独立 listen 后提供查询。
`closed` 表示 Hoopa 自己没有应用 listener/处理协程，不保证外部进程未占用同一端口。
SO_REUSEADDR 的 reserve 不构成排他占用证明。控制协议的版本及响应也不是 owner 证明；
管理器须在同一实际 TCP 连接证明 inode/进程实例后才发送任何凭据。

## 有限接口与传输

仅允许 `GET /hoopa/v1/status`、`GET /hoopa/v1/snapshot`、
`POST /hoopa/v1/activate`、`POST /hoopa/v1/drain`。
只接受 IPv4 loopback 对端及恰好一个正确的 `Authorization: Bearer …`。
禁止 query、转义路径别名、fragment、绝对 request URI、Transfer-Encoding 和 trailers。
GET 无 body；POST 必须有明确的正 Content-Length（不超过 4096）及单个
`Content-Type: application/json`。请求体读完且校验成功前没有状态变更。
无认证返回 401，未知端点/方法或绑定冲突 409，形状错误 400，终末开放/排空失败 503。
错误正文仅为固定文本，不包含原配置、token 或系统路径。

JSON 是 UTF-8 单一对象；拒绝重复/未知键、null、无效 Unicode、尾随 JSON、
不精确整数及缺少必需键。响应也按同一严格形状校验。响应体上限 64 KiB。
HTTP server 有有界头部、读、写、空闲超时；管理器保留原请求 context/deadline，
不通过环境代理、不重定向、不重连复用 owner 证明。一次证明可支持同一连接顺序查询，
连接断开后的重试必须重新证明；读查询不修复、不探测外部节点、不更新配置。

## 完整运行快照

`Snapshot` 字段（字段名为 wire 名）：

- `schema_version: 1`, `config_sha256`, `controller`（规范 `127.0.0.1:端口` 字符串，端口 1–65535）。
- `entries`: 按 name 升序，元素为 `name`, `type`（mixed）, `address`（明确 loopback
  host:port）, `auth_sha256`（实际使用的 users 数组按 profile.User 字段顺序 compact
  JSON 的 SHA-256）。不返回密码；数组描述用于创建实际入口的配置对象。
- `proxies`: 按 name 升序，包含**全部**已安装节点、逻辑组及 Mihomo 内置代理；每项
  为 `name`, `type`（Mihomo AdapterType.String）, `address`, `dialer_proxy`,
  `members`（有序数组，非组为 []）, `selected`（非组为空字符串）。从实际 tunnel
  map 和 selector 的 `Proxies()/Now()`（touch=false）读取，不返回期望值伪装观察。
- `rules`: 保持实际 tunnel 规则顺序；每项 `type`（RuleType.String）、`payload`、
  `target`。本版本不声称这些展示字段可以重建所有规则选项；完整原配置另由
  config_sha256 与固定受限解析/不可变配置能力绑定。

所有数组必须存在且不为 null。最多 4 entries、70 proxies（64 声明项及 6 内置项）、
64 rules、每组最多 64 members；GLOBAL 最多 66 members。name 为 1–128 ASCII ID；
字符串有界（binary_version 128 字节，rule payload 4096 字节），地址、digest 和引用严格校验。entry/controller 地址不得重复。
必须恰含 DIRECT/Direct、REJECT/Reject、REJECT-DROP/RejectDrop、COMPATIBLE/Compatible、
PASS/Pass、GLOBAL/Selector 六个内置项；其余为 node: 前缀 Direct/Http/Socks5 或 group:
前缀 Selector。只有 Http/Socks5 有非空 address（规范127.0.0.1端口），可选 dialer_proxy
仅引用声明项；其他代理的 address/dialer_proxy 为空。组成员仅引用声明项，GLOBAL
还允许 DIRECT/REJECT，selected 必在 members 中；members+dialer 依赖图不得成环。
规则 target 只允许 REJECT 或声明 node/group，不能借内置 DIRECT 形成额外回退。
规则 type 仅为 Domain/DomainSuffix/InName/IPCIDR/AND/Match；末项为唯一 Match 且
payload 为空，其余 payload 非空；InName 必引用实际 entry。AND 的显示 payload 不再解析。
管理器从已核验 bundle 的 `validation` 材料取得独立期望快照，逐项比较完整
entries/proxies/members/selected/rules 和 config SHA，不能用实际响应生成自己的期望。
该材料的生成及与配置语义的交叉校验归配置编译任务，原型使用固定人工可审 fixture。

快照 digest 是 Go `encoding/json.Marshal(Snapshot)` 紧凑字节的 SHA-256：字段声明
顺序即本节顺序，数组按上述次序；不含换行、运行状态、RunID 或版本字符串。
公共 `protocol/corecontrol` 定义该编码；核心候选使用逐字相同的纯协议源码副本，
冻结时核对哈希。每次查询重新读取实际只读投影；与启动冻结快照不一致则查询失败，
不把变化静默接受为新的配置。此版本禁止运行中修改 selector、reload、providers 或规则。

## 状态与操作

`Status` 必需字段：`schema_version:1`, `run_id`, `config_generation`,
`config_sha256`, `binary_version`, `mode:"basic"`, `capabilities`, `state`,
`snapshot_sha256`。capabilities 按字典序为
`authenticated-activate-v1`, `closed-start-v1`, `immutable-snapshot-v1`。
`binary_version` 取实际链接的 C.Version；它不是二进制 hash，也不是上游 commit。
存在已消费操作时带可选 `activation` / `drain` Result；没有时省略，不能写 null。
`state` 为 closed/open/draining/degraded。status 报告当前状态，Result 报告历史结果。
严格响应校验同时拒绝不可达组合：closed 没有回执；open 必须 activation=opened
且无 drain；draining 必须 drain=draining，activation 可无或 opened；degraded 允许
activation=failed 且无 drain、activation=opened 且 drain 无或 failed、或无 activation
且 drain=failed。历史 opened 可以保留在 draining，不能因此接受 closed 带已完成 drain。

`Mutation` 必需字段按顺序为 `schema_version:1`, `operation`（activate/drain）、
`operation_id`, `run_id`, `config_generation`, `config_sha256`, `snapshot_sha256`,
`registration_sha256`。digest 是 Marshal(Mutation) 的 SHA-256。registration SHA
仅是认证管理器交来的绑定；核心无法读取 Hoopa 的账本，不能声称独立验证了持久登记。
管理器必须先持久登记、核验完整实际快照，再提交 activate。操作 ID 为独立 ASCII ID。

`Result` 必需字段为 `schema_version:1`, `operation_id`, `request_sha256`,
`run_id`, `result`（opened/draining/failed）。运行内各只保存一次 activate 和 drain
的请求及结果；内存是该 RunID 的操作回执，不冒充跨进程持久操作日志。
相同操作、ID、完整内容重试返回原 HTTP 状态和 Result；相同 ID 异内容或另一个 ID
试图占用已经消费的同类操作返回 409。activate/drain 不能复用 ID。先验证绑定/形状，
被拒请求不消费槽位。Status 中两回执必须绑定相同 RunID、使用不同 operation_id；
activation 只容许 opened/failed，drain 只容许 draining/failed。管理器崩溃后可用相同请求查询/重试；核心退出后须根据原 pidfd、
birth/登记及持久操作日志对账，不能给新 RunID 重用原回执。

## 状态机与资源顺序

activate 只接受 closed：持 runtime mutex，重新核对快照与绑定，逐一把所有 app
reservation 转为 listener；**全部 Listen 成功前不启动任何 handler**。全部成功后，
在同一锁内记录成功回执、置 open、登记所有 Accept goroutine，再解锁。
因此多端口之间不承诺瞬时 TCP handshake 原子性；承诺任何失败开放都没有代理认证
响应、CONNECT/SOCKS 成功或有效载荷转发。若 Listen 中途失败，关闭本次所有已 listen
和未 listen 的 app FD，记录 failed/503，状态 degraded，此 RunID 永不重试开放。
外部占用者保持运行，不能为使开放成功而停止外部进程。

drain 可从 closed/open 进入 draining；关闭全部 app listener/reservation，不再接受
新应用。已经接纳的连接继续；关闭失败置 degraded、failed/503，不能伪装排空成功。
drain 后重投已成功 activate 可得到原 opened 回执，但**当前状态仍为 draining**，
不能重开入口。degraded 没有恢复开放操作。异常 Accept 错误也关闭新入口并置 degraded。
重复请求和 stop 与 activate/drain 由同一 mutex 串行化；关闭先禁止新状态迁移，再停止
HTTP、关闭本次 FD、等待已登记 Accept，最后有界等待/关闭已知应用连接。任何 WaitGroup
Wait 开始后不得新增 Add。manager 的正常退出与崩溃不调用核心 stop。

## 验收边界

先验证严格 wire、实际 socket 状态机、认证/绑定/未知接口、幂等/并发、部分 Listen
失败、drain 已有长连接与核心正常退出，再用新固定二进制验证真实 HTTP/SOCKS 流量
及管理器 owner/登记接线。保留旧候选原件与新 hash；仅新增基础模式分支，旧身份路径
定向回归及 G01–G03 影响由实际 diff 裁决，不能直接把旧 hash 的通过复制到新 hash。
本版本不包含完整身份模式激活、订阅任意协议、动态选择修改、自动升级或生产账本。

## 管理器适配边界

`internal/mihomo.Controller` 消费 Store 与受信本地 `CapabilityRecord`，由
`runtime.Ownership` 先提供已证明归属的原 TCP 连接。CapabilityRecord 必须固定
Binary path/version/sha256、linux/amd64、protocol=1、mode=basic、上述三个capabilities、
accepted=true及证据文件SHA；其Marshal摘要必须等于bundle的capabilities_digest。
这是一份经过安装/评审的本地声明，不是通过解析核心自己的响应来赋权；记录安装和
发放属于后续接线，单有可构造的Go类型不代表某候选已正式获准。

bundle须有且仅有一个 generated_config、固定kind=validation的
`controller-snapshot.json`，以及包含同一固定binary的清单。Inspector先核验全部
bundle/intent/digest，再从原配置提取secret/controller；期待快照严格解码并绑定
原配置SHA和controller地址，不能用响应补齐缺失验证材料。

Inspector通过同一连接依次查询status、snapshot、status；全部身份和快照绑定均须
匹配，完整快照逐项比较，以最后一次status的当前closed/open返回可登记观察。
不把draining/degraded认作可用运行；未来Stop/恢复协调器的状态判断不能直接等同于
这一可用性接口。只读操作没有默认修复、probe、重新连接或持久写入。

HTTP读取先限制8KiB响应头、64KiB明确Content-Length的JSON正文和2秒socket/context
预算；拒绝重定向、HTTP1.0、chunked、trailer、encoding和已经预读的尾随字节。
普通HTTP不能独立证明所有迟到响应的因果，仍依赖原连接owner与固定受信核心。
context能中断socket阶段，不承诺抢占同步文件系统ReadBundle；任何磁盘失败均不能
由controller响应绕过。本节当前为Inspector接口实现边界，尚未完成产品启动/激活接线。

## 管理器写操作适配（T026，实施中）

`Activate` / `Drain` 只能通过持有原 pidfd 的 `OwnedRun` 调用。先从仍持写锁且未
poisoned 的 Store 读取已有 registration，核对本句柄持有的 intent/birth 原件摘要，
再核验完整 argv、进程身份和本次实际连接的归属。读取不创建、补记或修复 registration；
只读 Store 不获得写操作通道。上层协调器负责在整个操作期间保持 writer 打开并串行
接纳，Store 不在网络 IO 期间持锁。缺少登记或证明时不发送认证或 POST。

一次调用使用同一连接完成三次 GET 完整核验、至多一次 POST，以及三次 GET 完整
事后核验；结束前再次验证原进程及 argv。操作绑定使用实际 registration 原字节摘要。
已知冲突在 POST 前拒绝；首次 activate 需要 closed 登记和当前 closed。
调用者可以显式重投同一请求；适配器自身没有自动重试、重连、换 ID 或恢复策略。
它没有替代 T027–T032 的持久 Operation、revision 和 active 发布。

POST 只接受符合有界 JSON 响应约束的 200/503；严格 Result 必须匹配 RunID、
OperationID、完整请求摘要。成功种类必须对应 activate/opened 或 drain/draining，
failed 必须对应 503。HTTP 错误、超时、断流或无效回执都不能证明请求没有产生影响。

`MutationOutcome` 分别保存已验证的历史 `Receipt` 和当前 `Current`。事后完整快照、
当前回执或最终进程证明失败时，Current 为空；已经验证的 Receipt 可保留供上层对账。
已验证 failed 回执返回独立的 transition-failed 错误。旧 activate 在 drain 后显式重投
可返回 opened 历史回执及 draining 当前状态；调用者不得由 Receipt.Result 推断入口开放。
现有 Inspector/Observe 的 closed/open 可用性限制保持；管理器重启后对 draining/degraded
的身份恢复接口尚待后续生命周期任务，不能把不可用当作进程不存在。

## 离线基础启动链

父管理器先核验同一 bundle/本地能力声明和固定可执行文件，消费首次创建 intent 所授予的
一次性出生目录 FD；专用 launcher 从当前管理器 `/proc/self/exe` 启动，显式 Setsid、
null stdin 和私有独占新建日志 stdout/stderr，仅继承出生目录。父进程立即安排其直接
child 的 Wait，并关闭自身继承 FD 副本；取消 context、关闭管理句柄或服务退出都不
主动停止核心。LaunchChild 的异步退出结果由独立状态和 channel 发布，复制/脱敏打印
句柄不读取尚未发布的结果。T033 才绑定正式固定内部 CLI 分派，不接受用户指定任意分派。

launcher 先 ClaimLauncher 持久化 birth 并关闭原目录 FD，才进入基础执行链。
argv 固定为 `[binary, -d, workdir, -f, workdir/config.json]`；工作目录和原配置文件须
通过已有私有路径/权限/单链接检查，原字节 SHA 必须匹配 intent。以目录 FD 切换 cwd，
再核对实际 canonical cwd。核心仍独立检查/解析同一原配置，任何失败都不能自动重发票据。

固定核心按 no-follow 逐组件打开，只接受 root/当前账户控制的安全祖先、普通单链接
可执行文件、非 setid/sticky、不可被组/其他人改写、最多128 MiB，核验其 SHA 和
amd64/64位小端 ELF，拒绝 PT_INTERP。通过**同一个已核验 FD**的 `/proc/self/fd/N`
执行，保留真实路径 argv[0]，CLOEXEC 防止该 FD 留入核心。执行前复核 inode、长度、
权限、mtime/ctime、owner及真实路径；不在 hash 后重新打开用户路径选取另一 inode。
这不隔离同 UID 恶意改写文件内容或 root/mount 攻击，仍须受信安装与权限边界。

核心环境由允许列表重建，只保留本次 nonce、RunID、generation/config SHA、协议字段
及指向私有工作目录的 HOME/TMPDIR。不继承父代理、BPF/tracing、Go运行参数或私密环境，
不修改父 shell。出生、执行、可用性、登记、激活是不同阶段，Start 成功不等于 ready。
