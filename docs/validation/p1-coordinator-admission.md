# T029 串行接纳：限定模块验收

2026-10-27。root 根据稳定源码、真实私有 Store 测试及独立引用核对，验收 T029。
本层接纳成功只产生 prepared/pending 收据；T030 的实际核心替换和 T031 的恢复尚未
完成，不能由这份报告推导产品 apply、C01 或完整 P1 已通过。

## 最终行为

协调器独占 writer Store，写流程与 Close 使用同一个控制锁。句柄的值副本共享内部
状态、锁和关闭状态；Close 等待在途接纳完成后才释放 flock。T030 必须在同一锁内
继续执行效果，不能把接纳返回的 created 当作锁外执行许可。

请求先查询已接纳身份；同 ID/同内容返回原已存记录，不因 revision 前进、依赖失效
或当前调用取消而重做效果。新请求严格核对 Plan、摘要确认、中断许可、当前 revision/
世代、所有历史记录及当前完整输入。任何 pending/degraded 操作阻止新 apply；损坏
历史明确报错。所有拒绝均发生在创建本次持久操作之前。

当前输入读取器还必须从已验证资料独立计算完整 impact。Plan 自报的受影响应用、
世代、运行行为及中断要求必须全部匹配；仅重算摘要不证明这些声明真实。当前 basic
完整替换没有已验的连接保留能力，replace/preserve_old 明确拒绝。

该读取器与 Intent 验证器是必需的内部受信接口；本轮测试使用预先声明的独立固定
输入，未实现完整来源编译或当前运行影响计算。T037–T041 及固定产品验收适配器须提供
真实实现，不能直接回显 Plan 充当观测。约束见[接纳契约](../../specs/001-hoopa-proxy-manager/contracts/coordinator-admission-v1.md)。

## 稳定版本验证

最终定向 race：8 顶层/59 子节点，67 pass、0 fail/skip。完整 control 包 race：
25 顶层/166 子节点，191 pass、0 fail/skip，无空 helper。vet、gofmt、空白检查通过。
所有检查使用固定 Go 1.27.3、离线依赖、host euid 1002 和私有 0700 临时目录；最终
33 项源码/fixture 输入事前保存并核对执行前后相同。

真实文件树比较覆盖拒绝前后的字节、大小、模式与纳秒 mtime；并发双副本只允许一次
首次接纳。阻塞当前输入读取时，Close 副本等待，实际第二个 writer 因 flock 失败；
关闭完成后可重开。测试还覆盖四种输入、编译器/能力、全部影响字段、取消、损坏/
缺失历史、旧请求先重放，以及伪造安全替换并重算摘要仍零写拒绝。

容量与 tombstone 的 1024 身份槽边界沿用 T027 已验 Journal 原语及本层调用路径审查；
本轮没有另建 1024 文件的协调器容量夹具，不宣称该路径有新增独立故障样本。
没有启动真实核心、VM、BPF 或完成运行提交，未执行产品死亡恢复矩阵。

初轮非对象 Plan 的错误分类已修正；测试对响应 wrapper 与损坏错误的假设也已更正。
随后审查发现独立 impact 复核缺失和复制 mutex 的风险，两者已修复并用新版测试验收。
早期允许 preserve_old 替换的正例明确过时，不能作为最终安全证明；旧结果与版本原件
保留。部分历史源码为按原摘要校验的事后重建，证据说明区分了它与事前捕获。

收据：`private/p1-coordinator-validation/executor/receipt.json`，SHA-256
`1f7ef81f88c730e70f37b1bf3bdc53d71db37c692b6abdfd56e7eaf1c5f8abd5`；freeze：
`d60ea65f6601274b05fafcbb5b527339bb2a6dd7626a20064ba8273945254c95`。
90 条冻结引用独立核对匹配；报告
`private/p1-coordinator-preparation/final-receipt-review.md` 的 SHA-256 为
`30abe4c05da11acef8cf0ae1235491fcecb416509f91e527bba2f47d2b0ab880`。

root 实现控制锁/接纳语义并作最终验收，executor 实际使用 gpt-6.1-sol/medium 编写与
运行测试，scout 实际使用 gpt-6-luna/high 核对源码和证据。没有新增第三方依赖。
