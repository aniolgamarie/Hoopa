# 持久操作日志 v1：T027 实现决定

2026-10-27，root。本文固定文件与重放边界；不表示后续协调器、应用或恢复已经实现。

## 请求和身份

`request_id` 保持 API 的 1–128 个 Unicode 字符不透明字符串，不收窄为文件名 token。
拒绝无效 UTF-8/孤立 surrogate；不做 Unicode normalization。磁盘键和 operation_id
统一为 `op-` 加 request_id 原 UTF-8 字节的 SHA-256，同一实例内稳定，不引入第二个
需要共同提交的索引文件。读取同时核对原 request_id 和键；摘要碰撞视为完整性错误。

请求摘要使用 RFC 8785 JCS，涵盖 `api_version`、`operation`、完整 `params` 三个字段，
request_id 单独绑定。对象键顺序和 JSON 空白不改变语义；数组顺序、所有参数、版本
及操作名均参与摘要。共享规范化原语提前用于 T027，T028 继续负责 Plan 的绑定及
仅排除顶层 plan_digest；不会因先有原语就勾选 T028。Hoopa 额外拒绝超安全整数。
输入上限 128 KiB、嵌套深度 64；重复键、无效 Unicode、非有限数和多余 JSON 值拒绝。
协议字段按精确大小写解码；这里不代替各 operation 的参数 schema 与授权检查。

只读 Lookup/Get 不创建、同步、修复或回收任何文件。Lookup 对原 ID 先比较完整摘要，
相同则返回已存阶段/结果，异内容返回 REQUEST_ID_REUSED；不检查当前 revision。
新写接纳的语义、revision/确认/能力检查由 T029 在单写入临界区完成。日志只接受
明确的持久变更名称；只读、probe、control-only 请求不能因此产生操作记录。

产品 `operation.recover` 的调用方请求也有独立去重身份/调用结果，完整 params 中的
operation_id 指向原目标操作；恢复仍推进原目标的阶段，不换其 ID、原请求摘要或
旧/新快照。T031 负责调用记录与目标记录的关联核验、分别持久化及中断消歧，不能
称为共同事务。P0 control-probe 的“recover 无独立调用去重”是明确的实验简化，
不直接转为产品语义；T027 能保存该名称并不表示恢复逻辑已经实现。

## 记录、更新与重放

每个已接纳操作是一份 `state/operations/<operation_id>.json`，包含不可变的请求
身份、base/target revision、旧/新 generation、candidate/impact 摘要，以及完整请求、
按序步骤证据和当前结果。target revision 可为 null（例如不发布全局 revision 的操作）。
request、每步 evidence/result 和最终 result 在内部文件中使用标准 JSON byte-slice 的
base64 封装，读取恢复精确规范化 UTF-8 字节，避免 RawMessage 二次转义 U+2028/U+2029。
压缩后的 request 为 null。该内部文件不是 CLI wire 对象；API 必须显式投影解码后的
JSON 正文。base64 仅为字节编码，目录/文件权限仍按私有存储要求执行。
进程证据可以引用 T025 的不可变 run/registration 摘要；这些引用本身不授权接管或发信号。

控制层严格解码并验证模型；config 仅提供有界私有文件读、列举和摘要 CAS 发布。
更新必须核对上次文件摘要，保留身份与已有步骤前缀；终态 committed/rolled_back
不可改写。degraded 保留全部正文，属于等待显式恢复的操作，不能压缩。
日志层仅校验 result/evidence 为严格有界 JSON 对象；具体响应 schema 与事实真实性由调用用例校验。
阶段合法推进仅约束记录形状，不证明核心效果；T030/T031 必须实际核验后才写终态。

步骤/结果及请求没有时间过期；Lookup 返回该原操作**最新已持久化**阶段/结果原字节，
不冻结为第一次 pending 回执，也不在查询时推进操作或生成新的成功。终态结果不再改变。
终态压缩只允许 committed/rolled_back，保留全部不可变身份、phase、最终结果及
正文原摘要，丢弃请求正文/步骤。压缩后相同请求仍重放完全相同结果，异内容仍拒绝。
未完成记录、degraded 和 tombstone 没有删除接口，不自动清理旧 ID。

## 容量与持久顺序

首版最多 1024 个身份槽位，每份记录最多 1 MiB；每个已接纳身份保留一个完整记录
额度，因此达到身份上限仍允许已有操作推进或压缩，而新 ID 返回
`OPERATION_CAPACITY_EXHAUSTED`（CLI 退出码 4，`retryable:false`）。不隐式清理或删除历史。
容量检查前仍先 Lookup 已接纳 ID：同内容精确重放已存回执，异内容仍返回
`REQUEST_ID_REUSED`，不因容量已满而改成容量错误。
这是最多 1 GiB 的逻辑记录预算，不预分配磁盘、不保证物理空间，不声称性能预算通过。
请求最多 128 KiB、结果最多 64 KiB、步骤的 evidence 与 result 原规范化字节合计最多 512 KiB（最多 128 步）；超限
拒绝本次更新并保留旧记录，不能截断或丢弃历史。压缩减少实际磁盘字节但不释放身份槽。
容量改变需显式版本/配置决定和验收，没有默认遗忘窗口。

在 Store mutex 和持久 writer flock 下，先核对既有记录/CAS/容量，再同目录创建独占
临时文件，Write → file Sync → Close → rename → operations 目录 Sync。
发布前失败保留旧权威；rename/父目录同步不确定则 poison writer，不能继续副作用。
显式 writer 重开已有 operations 目录同步屏障后，只读取/对账原记录，不补记成功。
poison writer 的 Get/Lookup 可返回观察到的记录，但同时返回 STORE_WRITE_UNCERTAIN，
不能把它作为成功重放或继续执行的依据。独立只读句柄仅报告当前文件观察；它不能
建立断电持久性屏障，任何恢复动作仍须显式 writer 重开与实际状态核验。
临时残留不是已接纳身份，查询忽略其正文；新写遇到残留时拒绝并要求显式维护，
不能在查询或重放中删除。T031 的显式普通 start/operation.recover 维护路径负责在
writer flock 下识别本协议独占临时名称、私有普通单链接文件并同步删除；T027 先报告
残留并拒绝写入，不提供隐式恢复。T031 必须在 config 包增加显式检查/维护接口，
control 不能越过 Store 私有 root 直接操作目录。损坏权威文件不属于可删除临时项，始终报错，不能
解释为没有记录。预接纳崩溃没有 operation_id 时，由显式普通 start 进入此维护。

操作文件、active 指针、运行登记和核心效果分别持久化，无跨文件/进程共同事务。
T029 必须在每次动作期间保持 writer 开启并串行协调；此日志不会跨网络持有 Store mutex。

## 有限验收范围

T027 验证：规范化/完整摘要、同内容重放与异内容拒绝、重开/压缩后结果保真、实际
填满身份容量后旧 ID 可读、新 ID 零写拒绝、未完成不可压缩、CAS 旧版本零写拒绝、
只读零写、损坏/私有路径拒绝、各持久边界失败与 uncertain writer 拒绝复用。
真实管理器死亡、运行态恢复、旧 revision 零写协调和 active 发布矩阵留给 T029–T034。

规范依据：[RFC 8785](https://www.rfc-editor.org/rfc/rfc8785)、data-model 第 3 节、
API v1 的 MutationReceipt 及 T023/C01。用户并未要求收窄 request_id 的字符集。
