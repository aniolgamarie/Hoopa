# T027 持久操作日志：限定模块验收

2026-10-27。root 根据稳定源码、定向故障测试和独立证据核对，验收 T027 并勾选。
这是日志模块的有限验收；协调器、实际核心效果和产品恢复矩阵尚未完成。
契约见[operation-journal-v1](../../specs/001-hoopa-proxy-manager/contracts/operation-journal-v1.md)。

## 实现与边界

- 请求 ID 保持 1–128 个 Unicode 字符，以原 UTF-8 字节的 SHA-256 形成安全 operation
  文件键；版本、操作名和完整参数参与 JCS 摘要。名称、数组顺序、revision、候选及确认
  变化不能重用旧 ID；对象键顺序与空白变化可以重放。
- Lookup/Get 仅读；已接纳重试返回原操作最新已存阶段/结果，终态结果固定。完整请求
  和步骤保留至明确终态；压缩只移除正文，保留去重身份、最终结果及旧正文摘要。
  degraded 不可压缩，没有删除 ID 或默认过期窗口。
- Store 的 operation CAS 在 writer flock/mutex 下核对旧摘要，同目录临时文件完成
  Write/Sync/Close、rename、父目录 Sync。发布不确定会 poison writer；读可返回观察
  字节同时携带 ErrUncertain，不能作为成功重放或继续执行依据。
- 最多 1024 个身份槽、单记录 1 MiB，达到身份容量仍允许旧操作推进；压缩释放实际
  字节而不释放身份槽。未发布残留不会被查询认领或删除，后续 T031 提供显式维护。
- 核心效果、revision/输入/授权复核和恢复分别由 T029–T034 负责；本日志不能证明
  result/evidence 所述网络事实。产品 recover 调用有自己的去重收据，恢复继续保留
  原目标操作身份；两个记录不构成共同事务。

## 已发现并修复的问题

独审发现已有 active 指针时，OpenStore 仍可能重建缺失的持久 operations 目录，
造成历史丢失后看起来是空历史。当前仅 pristine 初始化可建 bundles/operations/runs/
credentials；已有状态缺任一目录明确拒绝且不重建，新增四类回归。旧已完成存储阶段
的证据不被修改；这是本次新增证据触发的修复。

日志首轮测试发现两处终态压缩失败，原因相同：RawMessage 解码显式 null 后保留四字节
正文，而内存压缩模型使用 nil。已对显式 null 归一化并保留严格重编码检查；遗漏字段仍
拒绝。另把最终序列化/解码自检移到 CAS 之前，防止模型序列化不一致先成为权威文件。
原失败输出和失败时 control 源码保存在 `private/p1-operation-validation/attempt1/`。
更早一次测试编译的 ID 显式转换错误已修正，未发生日志测试运行。

后续补测又发现 encoding/json 的 RawMessage 路径会二次转义 U+2028/U+2029，即使
SetEscapeHTML(false) 仍发生；纯标准库诊断实际复现。root 改为在内部文件使用
标准 []byte/base64 保存 request/evidence/result，解码恢复原 JSON 字节；终态 null
也自然恢复 nil。保留真实 LS/PS 和字面量反斜杠-u 文本的独立预期，没有全局替换
转义或降低保真要求。该最终修改已经通过控制包复验；初轮失败及全部输入原件保留。

## 最终证据与适用版本

共同规范化原语按 RFC 8785 实现 UTF-16 键顺序、严格 Unicode/重复键拒绝和最短数字
表示；Hoopa 额外拒绝超安全整数。限定 race 5 顶层/91 子节点、96 pass、0 fail/skip，
vet/格式通过。输入限制含原空白；输出允许有限数字展开，日志再检查保存长度。
证据：`private/p1-canonical-validation/receipt.json`。这不是整个 Plan 编译验收。

root 日志/受影响 Store 定向 race：首轮 21 顶层/45 子节点，64 pass/2 fail/0 skip；
上述 null 修复后第二轮 66 pass/0 fail/0 skip，输入前后 hash 相同。随后移动了发布前
自检并补充边界测试，因此这两轮只作为历史证据，不代替最终版本检查。

最终检查按实际执行及源码版本分别保留：

| 包 | 对应执行 | 顶层/子节点 | pass / skip / fail |
|---|---|---|---|
| config | host-race-01，随后源码未变 | 49 / 334 | 383 / 0 / 0 |
| runtime | host-race-01，随后源码未变 | 14 / 20 | 34 / 0 / 0 |
| mihomo | host-race-01，随后源码未变 | 18 / 89 | 105 / 2 / 0 |
| control | 修复字节保真后的 host-control-race-02 | 9 / 13 | 22 / 0 / 0 |

该矩阵共 544 pass、2 skip、0 fail，其中四个空 helper 父节点单列，不将它们计为有
断言的测试；这不是单次四包全绿。初轮 control 的两个失败节点（父节点与 Unicode
子节点）原样保存，不与修复后的通过节点重复累计。相关 vet、gofmt 和空白检查通过。
新增边界覆盖请求/结果/步骤的精确上限与超限零写、终态压缩后 Unicode 与字面转义
保真、只读拒绝写、权限/损坏不冒充未知、发布后 sync 故障及 writer poison。

实际故障注入发生在 Store；Journal.Get/Lookup 对 ErrUncertain 的投影由源码审查
支持，没有宣称已经做独立的 Journal 注入。两个 skip 是实际核心 opt-in 测试，本轮
没有运行实际 Mihomo、VM 或 BPF，没有完成管理器死亡恢复矩阵或跨平台验收。

最终证据为 `private/p1-operation-validation/executor/receipt.json`
（SHA-256 `141d3ba9846207641fb708eb4ec407950d3def1df3218ae08a44738723e66c4e`）及
`freeze.json`（`40e1c752d0794430216dbe86c08a1ce0a3c435f1b6d18b069e7567778a121d6c`）。
独立核对 116 条 receipt 引用、115 条 freeze 引用和最终 63 个源码输入均匹配；报告
`private/p1-operation-preparation/final-receipt-review.md` 的 SHA-256 为
`43b552905302bd86f7e947bec0e3e60b2257380bb325aa02430aa6b2cde99b63`。
executor 收据保留“待 root 验收”的原状态，本文件及公开索引记录后续验收决定。

root 负责持久顺序与控制语义；executor gpt-6.1-sol/medium 实现独立 JCS 与限定测试，
scout gpt-6-luna/high 独立核对源码和职责边界。没有增加第三方依赖。
