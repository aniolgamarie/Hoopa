# T028 Plan 信封与摘要：限定组件验收

2026-10-27。root 根据稳定源码、独立摘要向量、最终测试和引用核对验收 T028。
本层不打开 Store、下载、探测或启动进程；完整 Intent/provider/rule 编译由 T037–T041
提供，实际 apply 由 T029–T034 实现与验收。

## 已实现的行为

`BuildPlan` 接受没有顶层摘要的严格八字段对象，`ParsePlan` 接受带摘要的完整九字段
对象。两者都要求纯 Intent 验证器；缺失或验证失败明确拒绝，错误不带原始诊断内容。
验证器收到独立的 Intent 与输入副本，Plan 内部保存规范化字节；读取结果也是副本。

Plan 绑定 revision、编译器、能力、全部固定输入、影响及 blockers。摘要仅排除顶层
plan_digest，嵌套同名字段仍参与摘要。对象键顺序不影响摘要，数组顺序参与摘要。
生成时 unknown/interrupt 强制要求中断许可；解析既有计划时不擅自修正这一字段。
仅计算摘要的函数不验证完整 Plan 语义，也不授予应用权限。

所有字段按大小、存在性、精确名称和类型检查；拒绝额外字段、重复输入 kind/id、
重复应用、null 数组及非法 JSON/Unicode/数值。原始和最终规范化 Plan 各限 128 KiB，
字符串的具体预算见[契约](../../specs/001-hoopa-proxy-manager/contracts/plan-envelope-v1.md)。
schema 允许 select/none 只表示形状合法，不表示 P1 已具有这些运行能力。

## 验证范围

三组摘要向量使用独立 Node 计算，包括 UTF-16 键顺序、数字表示、LS/PS、仅顶层排除；
公开 fixture 保存原输入和预期摘要，不从被测 Go 实现生成预期。Node 执行单独保存
真实退出码 0、输入前后摘要及输出一致性；最早没有单独记录退出码的准备记录保留。

定向检查覆盖全部输入与影响字段、validator 拒绝/副本修改、Build/Parse 差异、大小
边界、Unicode 及 XDG/环境无变更。实际无进程/网络效果还由生产导入与调用路径审查
支持，没有把它宣称为实际核心、VM 或 BPF 测试。

root 审查发现初版公开测试读取被忽略的 private fixture，已改为
`testdata/control/plan-digest-v1.json`，避免新 checkout 缺少测试输入。旧测试源码和
结果保留；生产 Plan 源码未因此修改。独立 scout 审查未发现结构校验绕过。

最终定向 race 为 8 顶层/94 子节点、102 pass，完整 control race 为 17 顶层/107 子节点、
124 pass；两次均无失败或跳过。相关 vet、gofmt 与空白检查通过，输入前后摘要一致。
完整包检查使用实际 host euid 1002 与私有 0700 临时目录。测试优先的未实现 API 编译
失败、早期准备错误及 private fixture 版本输出分别保留，不并入最终通过计数。

最终收据为 `private/p1-plan-validation/executor/receipt.json`，SHA-256
`ab126fbecea1b720da7c85115fae2aaf32266d851d39d975e9834667763454bb`；freeze 为
`945b790fe02435e0bf91c617104f92595d028048942103c03eeb238019bd2fab`。
独立核对的 82 个冻结引用均匹配，报告为
`private/p1-plan-preparation/final-receipt-review.md`，SHA-256
`b780babac354f74d7d643335d8b15a8b45782a5584d10f3db794a4620ef59938`。
早期测试源码是事后按原 hash 校验重建的副本，收据明确标注该来源，未冒称事前捕获。
T029 后新增协调器不在这次冻结输入内，其检查另行记录。

root 负责语义和最终验收，executor 实际使用 gpt-6.1-sol/medium 实现与测试，scout
实际使用 gpt-6-luna/high 核对边界和证据。没有新增第三方依赖。
