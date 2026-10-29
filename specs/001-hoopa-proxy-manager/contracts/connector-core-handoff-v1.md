# 有限核心准入与 exec 资源移交 v1

2026-10-24 冻结设计，实际结果另行记录。接续[在线实验契约](connector-online-spike-v1.md)；仍是 T132 的受控原型，不生成正式身份能力，不改旧 G01 0/100 或放行 T137。

## 所有权与范围

沿用独立 guest、初始 namespace、普通 IPv4 loopback TCP、root cgroup 两条 link、source 专用叶目录及只插入 maps 的实验边界。外部特权 detach/改写、TFO、原始报文、TCP_REPAIR、未知 BPF、长期回收及其他 namespace 尚未承诺。无宿主加载、TUN、系统代理或服务安装。

一次性 Python 加载器在当前进程 load/核验/freeze/attach，复制 6 个 map、3 个 program、2 个 link FD，进入叶 cgroup 后执行已固定摘要的 Mihomo ELF。使用 `exec` 保持 PID/进程启动实例及 FD 所有权；没有运行时 observer 或逐连接管理 RPC。FD 复制完成但 exec 失败时，加载器停止 writer 并关闭自有资源；exec 成功后原 Python 对象随地址空间替换消失，CLOEXEC 原 FD 关闭，仅显式移交 FD 存续。核心恢复这些 FD 的 CLOEXEC。

核心是这些 FD 的唯一用户态所有者。初始移交允许同一进程内的临时重复 FD；没有向其他进程发送 link/map，也不把 PID 存活或 marker 当作身份凭据。核心 SIGKILL 释放其 FD；管理器是另外的进程，管理器退出不得主动关闭核心 FD。该 exec 模式仅减少本轮交接边界，不等于生产权限部署、热重载或崩溃接管已经验收。

## 移交记录

环境变量 `HOOPA_IDENTITY_HANDOFF` 是最多 8192 B 的严格 JSON，不含秘密、客户端真值或应用正文；重复键、未知字段、非规范整数和额外尾部拒绝。

| 字段 | 类型与约束 |
| --- | --- |
| schema | `hoopa-core-identity-handoff-v1` |
| core_generation | 与受保护核心 generation 完全一致 |
| generation / source_cgroup_id / root_cgroup_id / netns_cookie | 非零 uint64 十进制字符串 |
| pidns_inode | 非零 uint32，实际 guest 初始 PID namespace inode |
| boot_id | 实际 guest 的规范小写 UUID |
| source_path | `/sys/fs/cgroup/hoopa-online-*` 的直接 domain 叶目录 |
| port | 1–65535，与唯一受保护入口一致 |
| maps / programs / links | 名称到 FD 整数的精确集合，FD 唯一且 200–1023 |

map 集合为 capture_config、connect_records、capture_faults、capture_gate、online_tuples、online_bindings；program 为 hoopa_connect、hoopa_lifecycle、hoopa_stop；link 为 hoopa_connect、hoopa_lifecycle。完整语法/schema/FD 集合检查通过才接管，之后任何 generation/port、环境或内核检查失败均关闭这 11 个已交 FD 一次，不执行未经核验的 stop 程序。语法不合法时不按输入中的任意数字关闭资源；启动失败的进程退出会由内核释放其余继承 FD。

启动检查实际 namespace、boot/PIDns、cgroup 位置及 ID；map ABI 与 FD 读写权限分别核对，program 关联 map ID、link 指向 program/root/attach type 必须吻合。配置 32 B 完全一致且更新被 EPERM 拒绝；hash 初始为空、fault/gate 初始为零。记录本身不是外部认证，信任链包括同一隔离运行中固定的加载器、对象、核心和操作记录。

## 原 accepted socket 与撤销

只允许一个受保护数据入口，继续使用原有 HTTP/SOCKS5 凭据与显式规则。核心在原 `Accept` 返回后、登记/启动协议 worker 前执行准入；单次 RawConn.Control 读取原 TCP socket 类型、两个 cookie 和两端点。查询只按原 accepted cookie，核对冻结配置、link、gate/fault、binding/C 后再次检查 gate/fault；不扫描 holder、猜时间、使用客户端声明或读 token。

Admit 与 Close 由同一 owner mutex 串行。任何身份读取/冲突/停止错误使该 owner 撤销后续准入；失败 accepted 由调用者关闭，不能进入协议读取。已有成功准入的连接携带不可变值继续走既定路由，不因另一个连接的后续 fault 改写归属。drain 先关闭新入口，已接受连接按受保护核心原语继续；最终停止先禁止新准入，再停止 writer、关闭 link/program/map，任何清理错误保留。

进程 TGID、leader 启动纳秒、boot/PIDns、caller TID 分列；comm 仍是 unverified 非原子快照、路径 unknown。继承与转交 FD 保持 connector。源准入成功只说明限定原型的连接检查通过，不直接开启 PROCESS/UID 规则或将三个正式 identity capability 改为 true。

## 验收顺序

1. Go 严格 ABI/移交/原 FD 与清理边界测试，不加载 BPF；完整字段值保持精度。
2. 新隔离核心接线并生成独立 0008 补丁/候选，旧 001–007 与旧 binary 保持。
3. 真正 HTTP CONNECT/SOCKS5 转发和独立目标回显，核对原始 connector；缺失/错误 FD、错世代、gate/fault/link 缺失不得读取代理请求或到达目标。
4. 来源绑定进入同一 flow 的版本化事件后，校验新事件形状、持久重开/重放与原帧，不能把旁路日志当作已入账事实。
5. 独立控制进程退出、核心 drain/旧连接续传与异常停止分别验收；有限单点资源与功能样本不代替正式预算或完整重启矩阵。

真实运行须事前登记输入/判据，保留所有失败与被拒绝阶段。新增协议若影响来源恢复或旧字段，须按实际改动重验；不得沿用旧 binary 的 passed 结论为新 hash 背书。
