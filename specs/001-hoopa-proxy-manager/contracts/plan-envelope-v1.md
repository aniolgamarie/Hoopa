# Plan 信封与摘要 v1：T028 实现边界

2026-10-27，root 决定。T027 后实现本层，T037–T041 提供完整 Intent/来源/策略编译。
本文不是 Plan 或产品 apply 已通过的声明。

## 严格信封

Plan 保持 plan-v1.schema.json 的全部必需字段、精确字段名、类型及额外字段拒绝。
base revision、generation 和输入 ID 使用既有 ASCII ID；输入 kind 限 provider、
core_binary、capabilities、rule_asset。同一 kind/id 不可重复；摘要必须为小写 SHA-256。
compiler version 非空有界，所有数组显式存在。blockers 不以默认空数组掩盖缺失。

T028 的公开资源预算为原始 JSON 与最终规范化 Plan 各不超过 128 KiB，嵌套深度沿用
CanonicalJSON 的 64 层容器限制。compiler_version 和 blocker.code 各为 1–128
个 UTF-8 字节，blocker.message 为 1–4096 个 UTF-8 字节；这是实现的有界输入约束，
不是已测得的实用规则/订阅规模。BuildPlan 输入省略顶层 plan_digest，ParsePlan
输入必须包含该字段；两者都拒绝 null 数组。只计算摘要的 ComputePlanDigest 接受
严格 JSON 对象，不构成完整 Plan 或 Intent 验证。

Intent 字段必须由**必需的纯语义验证器**校验；验证器接收独立 JSON 字节，只能使用
已传入的不可变内存输入，不打开 Store、下载、探测、启动进程或自行写候选。nil 或
不可用验证器拒绝生成/接受 Plan，不能以 JSON 对象或 manifest 摘要当作已编译证明。
该函数接口属于内部可信代码，不是允许用户提供的插件或绕过验证选项。
接口为 `IntentValidator(json.RawMessage, []PlanInput) error`，每次调用都交付独立
intent 和 inputs 副本；验证错误收敛为固定安全错误，不向调用方回显秘密。

T037–T041 负责真实 Intent schema/引用/能力、provider/node/group/rule 语义和输入
集合完整性。T028 负责严格信封、已经提供的固定输入绑定和完整摘要；固定 bundle
也必须由明确验证器绑定精确已验输入，旧 Mihomo config 不冒充 Intent 文档。
组件测试中的 schema/摘要 fixture 只证明 codec，不能升级为可应用的真实配置。

## 摘要与影响

共享 CanonicalJSON 先严格处理原始输入，删除且仅删除顶层 plan_digest，再做 JCS
和 SHA-256。嵌套同名键不会被遍历删除；Plan schema 仍分别决定嵌套字段是否合法。
解析既有 Plan 时不补字段或调整影响；摘要或结构不一致拒绝。生成 Plan 时可将未知
或 interrupt 影响确定为 requires_disruption=true，但不能将 unknown 改写成已知。

PlanDigest 不授权运行。应用仍须验证确认摘要、当前 revision、当前 compiler/capability
及**全部**输入、blockers 和中断许可；这些由 T029 在已接纳重放查找之后、首次写之前
串行执行。本层可比较已提供的输入集合，不负责读取或更新外部来源。P1 full apply
只允许 start/replace，select/none 的 schema 合法性不代表拥有对应运行能力。

## 纯计算与验证

本层不接受默认输出路径，不写文件、不初始化 XDG/state、不连接服务。CLI 后续只有
显式 --output 才能写调用方指定文件；普通 plan 仅返回对象。复制输入/输出切片，
不以调用方可变对象作为未来验证的权威。

验证覆盖独立 RFC/Node 摘要向量、键/数组顺序、仅顶层排除、全部参数与输入绑定、
必需 validator/错误透传为固定安全错误、字段缺失/未知/重复/null/非法 Unicode/数值
拒绝、unknown 中断、既有 Plan 不被自动修正、零文件/进程副作用。真实完整编译及
apply 故障矩阵在后续阶段验证，不由静态摘要推导。

## 后续版本边界

T048 在精确输入 kind 中增加独立 app_binding 承诺。T050 新 source/app 规划改用 schema_version=2 和[完整影响契约](plan-impact-v2.md)，保持v1字节／摘要的读取与既有请求重放。新source Apply在精确重放查找之后要求v2；不能把旧预览当成缺省完整影响。API transport仍为v1。本文其余T028历史向量及六字段impact规则保持原范围。
