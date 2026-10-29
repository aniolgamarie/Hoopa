# P1 运行归属记录 v1

状态：T025 实现契约，2026-10-27；不表示产品生命周期已通过。
遵循 [ADR013](../../../docs/adr/013-p1-runtime-ownership.md)。本记录独立于 SQLite、
操作日志和 active 指针；它们之间没有跨文件事务。

## 不可变记录与写入者

`state/runs/<sha256(RunID)>/` 保存 `intent.json`、`birth.json` 和
`registration-<sha256(OperationID)>.json`。记录使用文件存储 v1 的严格规范 JSON、
0700 目录和 0600 普通单链接文件，单份最多 64 KiB。所有可变应用配置/日志放在
intent 指定的工作目录，不混入控制记录目录。最多 1024 个 run 目录、每 run 最多
1024 份 registration；达到上限拒绝新记录，不删除未知或未完成运行。

manager 持有 state 写锁才能创建 intent 或 registration。intent 发布为新目录：
临时目录写文件/Sync、目录 Sync、rename、runs 父目录 Sync。同 RunID 再次准备
一律冲突，包括同内容；它不是“可以再次 spawn”的许可。读取已有 intent 只允许
观察和恢复，不生成新的启动票据。

birth 是唯一委派给 launcher 的写槽。manager 在 intent 持久后才能把该 run 目录
的 FD 传给自有 launcher。launcher 用继承 FD 核验 intent 摘要和 nonce 摘要，
取得自身真实 PID/boot/start，先持久出生记录，再以同一 PID exec 核心；不取得或
继承 state 写锁。该 FD 只用于这个 run 的出生槽，exec 前关闭。普通 manager API
不提供任意 birth 写入。launcher 重入、已有 birth（即使内容相同）一律拒绝 exec。

birth 用临时文件写/Sync 并成功关闭写 FD，再通过同目录 hard link 无覆盖安装到 `birth.json`，
移除临时名、目录 Sync，最后才 exec。此处 hard link 是原子“不覆盖已存在名称”
发布，不是允许最终多链接记录；任一未完成/多链接/Sync 错误保留为冲突，禁止 exec。
尚无 birth、出生后尚未 exec 或进程不可见，都不能推导“从未启动”，也不能重试 spawn。
准备 API 只在首次成功持久 intent 时返回一次性的内存启动票据；已有 intent 的读取
不能恢复这张票据。票据只能领取一次供 launcher 继承的目录 FD。即使重复启动了
launcher，出生槽的无覆盖发布也只允许一个实例进入 exec。

registration 在 manager 取得完整实际进程和同连接 controller 证明后持久化。
按当前 OperationID 保存一份不可变记录；同内容重写幂等，异内容冲突，不覆盖旧登记。
新的恢复操作可为同一 RunID 保存新登记，必须重新实测；原出生操作仍保持原值。
同操作重试也先重新实测身份和配置；若原登记已存在，保留它当时的 gate 快照，
以本次观察单独表达当前 gate，不能把 closed 登记改写成开放之后的运行历史。
rename/目录 Sync 结果不确定使 manager Store poisoned；显式重开先同步 run 目录，
核对既有记录后才能继续，不能把重开当成已完成核心恢复。
当前使用上述条目/单记录上限，不宣称提供总磁盘字节配额；容量和清理策略仍由后续
操作维护任务实现，未完成记录不会被静默清理。

## 字段与校验

`RunIntent`：version=1、run_id、operation_id、generation、manifest_sha256、
binary(path/version/sha256)、workdir、arguments、controller、config_sha256、nonce_sha256。
RunID/operation/generation 用既有有界 ID 格式；所有摘要为 64 位小写 hex。
workdir 和 binary.path 是规范绝对路径，最多 4096 字节，不含 NUL。
arguments 是完整 exec argv（第一个必须精确为 binary.path），1–64 项、每项最多
4096 字节、总计最多 32 KiB，不含 NUL；不保存应用 argv 或业务正文，核心秘密只通过
私有文件引用，不放参数。controller 只接受规范 `127.0.0.1:<1..65535>`。
config_sha256 必须匹配绑定 bundle 中一份 generated_config 文件；binary 必须与
bundle 的一份固定引用完全相等。存储验证不代表该配置具备可运行/closed-start 能力。
nonce 原值为每次新 spawn 独立生成的 32 随机字节小写 hex；这里只持久其 SHA256，
launcher/核心通过受控 `HOOPA_RUN_NONCE` 环境取得原值，诊断不得输出原值。

`RunBirth`：version=1、run_id、intent_sha256、pid、uid、boot_id、start_ticks。
pid 为 1..2147483647，uid 为 uint32，boot_id 为规范小写 UUID，start_ticks 为
非零 uint64 十进制字符串。它只证明 exec 前出生链，不是信号或接管许可。

`RunRegistration`：version=1、run_id、operation_id（本次核验操作）、intent_sha256、
birth_sha256、pid、uid、boot_id、start_ticks、generation、manifest_sha256、
binary_sha256、config_sha256、nonce_sha256、controller、gate（closed/open）。
字段须与原 intent/birth 完整对应；本次 operation 可与出生 operation 不同。
同一 PID/start/boot 的复核还必须实际匹配 exe path/hash、cwd、nonce 和完整 argv；
同连接 controller 认证/status/完整配置的验证由 T026 实现。登记保存验证结果，读取
登记本身不重新授予任何进程控制权。首次启动要求实测 closed，恢复已有运行按实际
gate 记录；只有后续完成全部前置条件的显式 activate 才能开放入口。

## 失败与恢复

- intent 未持久不允许 spawn；发布后崩溃但没有 birth 返回 unverified/conflict。
- birth 持久后 exec 失败也保留出生记录，不能重复 exec 或按同名进程重新绑定。
- 凭记录中的 PID 打开新的 pidfd 后，必须核对原 birth 与实际完整身份；原始 pidfd
  在本次连接归属证明前后都有效，才可写 registration。
- registration 写失败不关闭/杀死核心，也不开放入口；保存冲突并交显式恢复处理。
- 所有只读打开/读取不创建目录、不补记 birth/registration、不修权限、不联网或发信号。
- 本契约不实现操作去重、active 发布、stop/restart 策略或产品自启动，这些仍由各自任务
  与唯一协调器完成；不能根据本文件存在宣称 T025/T034 整体验收通过。
