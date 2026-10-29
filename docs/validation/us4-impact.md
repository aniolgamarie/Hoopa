# US4 完整影响预览

2026-10-29，T050 的纯投影、Plan v2 codec/schema、实际存储捕获与新旧请求处理已通过限定模块验证；真实 ELF 的 Plan 检查在隔离 Linux 中普通与 race 均通过，最终证据复核通过，T050按该有限范围完成，累计64/140。T051 的实际应用转发、部分入口失败回滚及管理器死亡矩阵尚未执行。

新 source/app Plan 使用 schema_version=2；API transport 仍为 v1。预览列出上一代和目标代引用的来源／节点、组、规则及 managed 应用并集，包含被移除的应用，区分存量连接中断与激活后的新连接。它展示配置影响，不把自动组候选冒充当前选中节点，也不推断连接迁移。回滚目标明确为首次启动失败时停止候选，或恢复准确旧 generation 与 manifest SHA。

上一代来自锁内读取的实际 active bundle，不能用最新下载的 provider 或新 Intent 代替。已支持的 source/app bundle 必须有完整且一致的 route map／Intent／生成配置；缺失、损坏或身份不符直接失败。只对已批准的较旧 basic-v1 配置明确标注 previous metadata unknown，保留回滚身份并显示已知并集，不能把读取失败伪装为空集合。

旧 Plan v1 精确字段和摘要向量保持可读。旧请求先按 request ID/digest 重放原有记录；新 source Apply 在重放之后要求 v2，并在写操作／工作目录之前重新捕获、编译和比较全部影响。用户自行重算摘要不能使篡改的 rollback target 或缺失的影响项通过。

| 范围 | 普通／race 每次结果 | 限制 |
| --- | --- | --- |
| 纯影响投影 | 4 顶层／57 项通过 | 元数据并集、完整身份、unknown、所有权，不读运行态 |
| codec／schema | 17 顶层／274 项通过；离线 schema 2 个元校验／93 个实例 | 保留 v1 向量；排序、身份等式和当前权威绑定由 Go 验证 |
| 当前 capture／协调器组合 | 62 顶层／589 项通过 | 真实私有文件及新旧请求；历史运行使用标明的 synthetic fixture |
| 隔离 Linux 实际 ELF／Plan | 12 顶层／37 项通过 | 固定 c90 ELF 真实复制／打开，不启动核心；旧 active 是配置元数据夹具 |

各批零失败／跳过，相关 vet 通过，范围有重叠，不相加充当唯一测试总数。VM 无网卡或宿主共享挂载，两次测试和 QEMU 均退出0、SHA导出帧验证通过，无强制收尾。宿主 /tmp 容量限制未通过放宽路径保护处理；构建在仓库私有目录，实际大 ELF 检查在已有隔离 VM 中执行。

独立复核与固定输入、日志见机器索引（本地原件 `../../experiments/gost-mihomo/us4-impact-validation.json`，不随Git上传）。这些结果不证明实际应用路由、已完成回滚、原始进程身份、计量、其他平台或性能预算；后续 T051 会验证实际产品行为。
