# G01 源进程与短连接：受限source5通过

2026-10-26当前评审：T012消费[T127正式证据](source5-formal-identity.md)及[验收决定](../../experiments/mihomo-events/source5-identity-acceptance.json)，不重新采样，按connect_initiator_v1接受固定0017、Linux/amd64、初始namespace、受控cgroup/IPv4 loopback的受限G01。正向一次性登记100条并100条正确，原socket在路由前绑定原始PID/TGID、启动身份及程序快照；每条终末33/33，来源seq/flow/epoch独立核对。普通、子孙、父connect/子写、SCM转交及线程各20，两个域名/两实际节点均有原始证据。同名不合并启动实例，FD转交不改历史connector；该运行能力不声称识别所有后续writer。

| 单独边界 | 通过结果 | 判断范围 |
| --- | --- | --- |
| PID复用 | 2/2 | 同PID30000不同启动身份/cookie，不串账 |
| 凭据缺失 | 2/2 | 一条基线、一条缺exe记录拒绝；未admit无来源账，保留null |
| 权限 | 1/1 | 降权后的有效BPF管理操作EPERM；不扩大为G07或任意来源可见性 |
| 三值规则 | 14/14 | 真实非法字节字段、AND两顺序；6转发33/33、8已admit拒绝0/0 |

这对应F20/F21的实例与转交边界及F22的100短流终末子范围；采集离线、来源重启、未送达重放和恢复缺口仍由T128/G02验证。本次private证据与公共report的总入口实际race1通过0失败0跳过，权限实验真实执行，未把skip算作通过。P0/P1、生产部署和其他平台未放行。

下文保留2026-10-22的旧GOST和原生候选结论；旧0/100、首轮source5 60/100均未被改判，旧共享拒绝oracle不套用到新连接发起者口径。

## 历史评审（2026-10-22）


日期：2026-10-22。本轮取得原生能力的反例和缺失证据；未完成 100 条真实短流及 F20–F22 全矩阵，T012 保持未勾选。

已定义并执行 [identity_test.go](../../experiments/gost-mihomo/identity_test.go) 的严格 oracle：100 条预登记合成样本要求真实源 socket、PID、名称、启动身份、boot、目标与终末字节；缺记录/晚到身份为 unverified，转发器冒用、PID 复用串账和错误目标拒绝。合成测试通过只证明断言会拒绝坏数据。

真实证据见摘要（本地原件 `p0-2026-10-22.summary.json`，不随Git上传）：Mihomo 直接校准时识别实验客户端，经过 GOST 后活动 metadata 的 process 为 `gost`；真实 Codex 工作流亦如此。Mihomo 活动快照不能替代 GOST 之前的原始源身份。

仅实验了一个已有扩展点 `recorder.service.handler` 的 HTTP plugin，20 条终末记录均有 SID、地址、目标及整数 input/output，但没有 PID、进程名、启动身份、持久 seq 或 source epoch。地址元数据并不构成已验证的同步进程归属；没有在路由前得到身份与跨跳共同标识。

仓库 fixture 中真实 OS 身份仍须运行前登记；没有用假 PID、run ID、入口/service 名或 GOST PID 填入。100 条真实短流、两个同名进程、子孙进程、共享 socket、PID 复用与停采后重放未在当前候选上完整执行，不把它们改判为“不可识别样本”以缩小分母。

当前判定：原生候选不足以满足源进程保证，G01 unverified，不能交付完整进程/域名账本。整改要求和被排除的做法见 [ADR 001](../adr/001-event-source.md)。

## 重规划后的追加证据

[原入口探针](../../experiments/sourceidentity/README.md)已实现并尝试100连接；共享FD拒绝通过，100来源断言因同UID FD目录不可读未验证，没有通过跳过不可见进程来凑100%。该限定方法不作为通用生产方案；按[ADR 005](../adr/005-telemetry-replan.md)转向同一数据面身份/事件能力，完整G01保持未关闭。
