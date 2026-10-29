# Protected core v1：T135 受保护实验入口

状态：Linux/amd64、IPv4 loopback 的显式实验 profile。它约束隔离 Mihomo 候选，不是产品 CLI、通用 Mihomo 配置格式或正式 G05/G07 放行。原始 connector 后端仍未验证，所有进程实例、名称、路径规则能力均为 false。

## 启用与固定输入

设置 `HOOPA_GUARD_CONFIG_SHA256` 和 `HOOPA_GUARD_GENERATION` 后进入独立启动路径。任一变量存在即要求完整有效输入；不能凭空值回落到普通启动。以 `-d ABS_PRIVATE_DIR -f ABS_CONFIG_JSON` 指定已有私有目录与文件：目录必须当前用户所有、0700、无路径符号链接；文件必须当前用户所有、普通文件、0600，以 NOFOLLOW 读取，最大 64 KiB。配置只读取一次，SHA256 校验、纯能力校验和核心 Parse 消费同一份字节。

不接受 base64/stdin 配置、controller/UI/secret CLI 覆盖、post-up/down 脚本、geodata 开关或位置参数。已注册参数进入 protected 路径后的失败只输出固定错误 `Hoopa protected configuration rejected`，不附带配置或凭据；未知 CLI 参数仍由原生 flag parser 处理。`-v` 为既有版本查询；合法 `-t` 完成配置解析后退出，不启动入口或来源采集，不代替真实启动验收。

## 有界配置能力

校验实现位于 [capabilityprofile](../../../experiments/mihomo-events/capabilityprofile/profile.go)，核心携带其逐字相同的纯生产文本。递归拒绝未知/重复键、null、错误类型和越界数值，保留规则与成员原序。主要范围如下：

| 配置 | 已实现范围 |
| --- | --- |
| 基础 | 显式 rule 模式、silent 日志、IPv6=false、find-process-mode=off；profile 的 store-selected/store-fake-ip 均 false |
| 管理入口 | 一个 `127.0.0.1` 非零 TCP 端口，独立 controller secret；不启动上游 controller/API |
| 代理入口 | 1–4 个命名 mixed TCP 入口，明确 `127.0.0.1`、非零端口、udp=false，每个入口恰一组 username/password |
| 凭据 | 密码和 controller secret 为 32–128 个可见 ASCII 字符，拒绝冒号/空白/控制字符；用户名为有限 token 且不含冒号；入口密码不能等于 controller secret，不同入口不能复用同一用户名/密码对。凭据限定到各入口，并非跨入口的进程身份 |
| 节点与组 | 最多 64 个持久 `node:`/`group:` 引用；DIRECT、明文 HTTP、SOCKS5 节点及 select 组。代理端点为不同的 `127.0.0.1` 端口，完整核对引用、成员、循环和最多 8 跳链；无动态 provider、缓存恢复或隐式候选 |
| hosts | 可选显式域名到 `127.0.0.1` 的实验映射 |
| 规则 | 1–64 条，支持 DOMAIN、DOMAIN-SUFFIX、IPv4 IP-CIDR、IN-NAME、单层 AND（2–8 个原子条件）；末条必须显式 MATCH。目标为已存在的 node/group 或 REJECT，不接受隐式 DIRECT fallback |
| 未支持能力 | 所有 PROCESS/UID、OR/NOT、规则集/子规则、其他条件组合、命名入口 proxy/rule 快路径、TUN、普通 mixed-port、其他监听、UDP、TLS 上游及未经声明的字段均在配置加载期拒绝；不可达规则也不豁免 |

首匹配与 AND 不重排。未支持字段在加载时拒绝，与运行中字段 unknown 不同：本 profile 不接受进程规则，不能通过“所有连接永久 unknown/reject”冒充进程规则能力。既有三值规则单测仍保留，真实 connector 降级矩阵由 T132/T129 验收。

## 启动、代际与失败边界

顺序为：私有文件/hash → 全部能力校验 → 同字节核心 Parse → 可选 trace 的代际/映射/来源可写校验 → 应用核心路由但不创建原生监听/controller → 对所有入口及管理端口只 bind、不 listen → 全部预留成功后转换 listener → 才启动协议处理并发布 running。

晚出现的 bind 冲突不会提前启动前面的端口。任何预备失败关闭本实例全部已预留/创建的 socket；不存在跨多个 listener 的内核原子事务，listener 转换阶段的错误仍通过关闭全部资源处理，协议处理始终在整体准备完成后启动。没有修改另一个已运行核心的配置或入口，因此失败候选不能声称已经回滚了外部进程。

可选计量仍使用原 `HOOPA_TRACE_*` 参数，但必须指定现有入口、profile=2、与 guard 相同的 generation，以及覆盖全部节点/组且类型一致的固定 route map。源目录必须能创建新 epoch 并为首流预留计量空间；损坏、只读重放模式、epoch/容量上限或映射失败均拒绝开放入口。来源恢复允许保存私有派生材料，拒绝启动不意味着没有私有文件写入；不因此产生已接流或已应用的成功声明。

运行中的 generation 与配置原字节 SHA256 固定在本进程内；SIGHUP 不重新读取文件，不能改变 generation。跨进程的 generation 唯一分配与持久变更日志仍由 T136/后续管理器承担。

## 认证与只读控制

入口沿用核心 HTTP/SOCKS5 协议实现；protected 模式下每个 HTTP 请求独立验证凭据，之前成功的 keepalive 请求不能授权后续无凭据/错误凭据请求。SOCKS5 必须协商用户名/密码，只允许合法 CONNECT，拒绝 UDP ASSOCIATE/BIND。非空密码使 SOCKS4 不能通过本 profile 的认证。没有创建 UDP socket。

控制面只接受 `127.0.0.1` TCP 来源及恰一个正确 `Authorization: Bearer ...`，未授权先返回 401；以下路径要求空 body、无 query：

| 方法/路径 | 行为 |
| --- | --- |
| GET /hoopa/status | 只读返回 schema_version=1、config_generation、config_sha256、state、accepted_tcp_connections、三个为 false 的 identity 能力。accepted_tcp_connections 是当前持有的连接数（len(r.conns)），不是累计 accept 次数；不会修复、选路或更新 |
| POST /hoopa/drain | 停止本进程全部入口的新 accept，保留已接受 TCP；正常返回 draining，重复调用幂等；关闭失败返回 degraded/503 |

其他路径/方法在授权后返回 409 `full replace required`，包括配置修改、selector、规则禁用、provider 更新、restart 和主动 delay GET；不会调用原生 controller handler。无凭据不返回受保护状态。loopback、私有文件和凭据不构成同用户下的 OS 安全隔离。

## 排空与完整替换

drain 的线性化点由同一锁保护：状态变化后不再登记新 accepted TCP；已登记连接仍属于旧 generation，持续转发直到客户端结束或核心停止。已接受的 HTTP keepalive TCP 可继续提交请求，但每次仍需认证；排空不承诺逐 HTTP 请求切断或迁移。

完整替换使用独立、已校验的新进程/入口；旧入口可处于 draining，旧流不迁移到新规则。T135 的隔离验收只证明上述原语；控制进程崩溃接管、持久操作身份、跨资源分阶段应用和完整回滚矩阵属于 T136 及正式门槛。
