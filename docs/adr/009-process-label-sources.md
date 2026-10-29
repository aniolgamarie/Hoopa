# ADR009：程序名、路径与内核进程名分别表达

状态：候选设计，2026-10-24。已取得独立快照实验及新v4核心的有限正向结果，尚未冻结正式字段能力或改变 G01 可识别样本。本文是工程评估，不记录为用户已经选择的新澄清答案。

同日后续决定：[ADR010](010-restricted-identity-profile.md)选定区分 executable 程序名/路径、kernel comm 和应用显示名的工程方向，用于下一受限候选。下文保留最初候选的理由与边界；字段协议、能力启用和正式验收仍待完成，不改变旧数据。

## 问题与参考

用户需要按进程实例查看流量、按同名进程合并，并按进程/应用指定线路。“名称”必须有明确来源，否则 kernel comm、可执行文件名和应用显示名可能被混为一个字段。当前 leader/caller comm 只有非原子观察；已实测 ftrace 关闭期间会漏掉改名而没有 recursion_misses。因此不能把该观察提升为精确名称规则的可靠输入。

已固定的 Mihomo v1.19.26 源码（commit `fc8c5a24b16991f98cd736950c17d1aa306a5041`）提供了有用的名称语义参照：Linux 非 Android 分支按 socket inode 查 `/proc/<pid>/exe`，tunnel 将返回路径的 `filepath.Base(path)` 赋给 `metadata.Process`，完整路径赋给 `ProcessPath`。参见[该版本 Linux 来源](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/component/process/process_linux.go)和[该版本字段赋值](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/tunnel/tunnel.go)。这只说明名称取自何处；其事后 proc 扫描不能替代 Hoopa 所需的原始 connector 证明，也不表示审查了最新上游。

Hoopa 的[独立 exe_file 实测](../validation/connector-executable-snapshot.md)和[路径边界](../validation/connector-executable-path-boundaries.md)已证明固定 guest 上可以在 connect 内获取带引用的文件、渲染路径并向后续 gate 提供同 cookie 值副本。它避免以当前进程回查覆盖旧连接，但每次 connect 的消费语义、完整关联和部署范围仍需补齐。

## 候选字段与使用边界

| 字段 | 来源与含义 | 后续用途 |
| --- | --- | --- |
| connector 实例 | 原始 TCP 发起者的 PID、leader 启动身份、坐标与世代 | 进程实例账本和实例规则的主键 |
| 程序名 | 已验证路径快照的 basename，携带来源版本 | 同程序名合并、明确选择该来源的名称规则 |
| 程序路径 | exe_file/d_path 在建连过程中的有界原始路径字节 | 路径展示与适用的精确路径规则 |
| leader comm / caller comm | 各自独立的内核名称观察和可信状态 | 诊断和可明确标注来源的展示；未验证时不能加载依赖它的精确规则 |
| 应用显示名 | managed 应用配置，例如用户给入口命名“Codex” | 应用层分组；不能代替操作系统进程身份或证明后续逐字节写入者 |

如果采用该 profile，API、规则、排序/合并键和历史记录必须保存 `name_source` 与版本，不能把不同来源的同一文本静默合并。历史名称取自原连接快照，不随当前文件改名、进程 exec、FD 转交或配置显示名改变而回填。解释器路径代表解释器，不能据此猜 Python 脚本或应用包名称。

路径错误必须保留 errno 和字段不可用状态，不能截断后作为成功值。非法 UTF-8 保留原 bytes，展示转义；产品匹配语义需在协议冻结时明确。` (deleted)` 是内核渲染的有歧义文本，不能随手去掉或冒充当前文件存在；当前实验中的独立 stat 不是未来生产事件已经包含的证据。路径依调用者的根目录/挂载视图解释，不能仅凭字符串当成永久文件身份。

## 接入前必须完成

先验证同 socket 重试、未消费旧记录及挂接连续性的边界，再确定 name/path 能力的允许条件和缺失拒绝策略。接入时使用新协议 profile/原始帧和候选 manifest，声明程序名来源，并以线程、exec/改名、FD 转交、短流和两线路的实际事件验证。旧 v3 comm 字段、旧候选及旧 G01 0/100 不重新解释。

只有新 profile 和运行拓扑通过预检后才登记新的正式样本；不得把当前两个独立路径探针算作 T132 已完成。若产品仍要求以精确 kernel comm 匹配，则它保持独立能力门槛；新增程序名不能悄悄冒充该能力。

## 组合接线进展

[未消费旧记录反例](../validation/connector-executable-unconsumed.md)证明单独CAS不足；后续[同调用编号实验](../validation/connector-executable-call-token.md)及[source v4](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v4.md)加入当前native connect编号、一次消费和296B原始证据。[001–012组合核心正向运行](../validation/connector-executable-core-positive.md)已在固定隔离guest核对两条流的原进程、路径/程序名、实际节点、ready与终末。首次0011遗漏质量捕获开关而整轮失败，原记录保留。

接线已存在，但这些证据仍不足以宣告正式程序名/路径能力。下一批按[原连接边界契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-core-edges-v1.md)验证线程、FD转交、comm改名及换路径exec，随后在同一新组合补负向与连续性矩阵。所有能力记录仍为unverified/false；旧字段、旧候选和正式样本不重解释。
