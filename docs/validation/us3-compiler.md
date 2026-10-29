# US3：完整来源编译与受保护自动组

2026-10-28，T041 的有限实现已完成组合验证。权威机器记录见 us3-compiler-validation.json（本地原件 `../../experiments/gost-mihomo/us3-compiler-validation.json`，不随Git上传）；设计边界见 [sources-v2 契约](../../specs/001-hoopa-proxy-manager/contracts/source-compiler-core-v2.md)。这是共享入口的编译与核心接线验收，订阅命令、持久调度与完整两来源矩阵仍由 T042–T044 完成。

`policy.Compile` 从严格 Intent、已验证的私有来源快照、固定二进制/能力和显式入口凭据生成完整 bundle，不读取网络或探测节点。资产包含原始订阅、解析身份和完整本地地区规则；规则、route map、生成配置及独立预期快照一起绑定 Plan 摘要。公开 getter 返回副本，失败不返回可用的部分 bundle。未知地区不补猜，失效 pin 不换节点；跟随组保留稳定本地 ID 和父组关系。

现有 basic-v1 的保护范围不足以接纳远端节点和 URL-test。新增的独立 sources-v2 协议及 [0020 文本补丁](../../experiments/mihomo-events/patches/0020-sources-control-v2.patch)保留旧协议，只对固定新二进制授予有限能力。自动组快照记录候选与探测策略，不把实时选中节点固定成配置事实。节点必须有该策略下成功、未过期的探测；没有成功样本或全部失败时返回 REJECT。跟随组使用同一父组，不另选首节点或其他供应商。延迟为节点探测延迟，不表示应用连接 ready 耗时或最佳业务质量。

US5 根据完整 BinaryReference 选择固定能力，再严格检查对应版本。sources-v2 入库前重新从生成配置投影完整快照，核对成员、策略、入口认证与 controller；启动后仍须通过实际核心快照及原进程身份检查。原有操作日志、发布顺序和 pidfd 归属要求保留。

| 验证范围 | 结果与边界 |
| --- | --- |
| sourcecontrol codec | 最终 race 198 PASS；旧版本混淆、策略与图约束拒绝。普通测试在此前同模块阶段通过，最后 URL 原文边界补测在最终 race 中覆盖 |
| sourceprofile / 独立投影 | 普通、race 各 77 PASS；七类有限协议的规范化配置与本机回绕拒绝 |
| 原子探测与自动选择 | 普通、race 各 41 PASS；实际 HEAD/503/超时/取消，零毫秒、过期、时钟回退和溢出为合成测试 |
| 编译与 bundle | 普通、race 分别各 34 + 4 PASS；来源身份、全部资产、容量、确定性与副本隔离 |
| Controller 版本接线 | 新测试普通、race 各 54 PASS；旧有定向测试各 126 PASS |
| BasicMaterial 准入 | 普通、race 各 49 PASS，0 跳过；正例仅做真实静态 ELF/procfs 只读检查，负例无部分权威或写入 |
| 固定静态核心 | 最终 native 13 PASS，0 跳过；13 个实际进程含 9 次拒绝启动、41 个构造/快照变体、两节点自动切换与跟随、全失败拒绝及重开 |
| 完整产品组合 | 最终普通、race 各 1 场通过；每场 2 个核心、4 次实际 HTTP 代理转发、两次公开 Stop，原 pidfd 退出且 proc 消失，无清理补信号 |
| 受影响生命周期回归 | 宿主 race 186 PASS，5 个未提供旧核心变量的可选真实测试跳过；不把跳过计作通过。注册表另有 race 4 PASS，最终相关包 vet 通过 |

组合场景使用真实 `subscription.Parse → policy.Compile → BuildPlan → BasicApplier.Apply`。协调器关闭期间核心继续转发，重开后普通 start 接管同一原身份；显式 Recover 复核，Stop 停止，Start 从已封存 bundle 创建新实例，不再次下载或编译来源。本场景是协调器关闭/重开，并未向管理器发送 SIGKILL；旧核心的 US5 故障矩阵不能自动成为这个新二进制的实测结果。

新核心 SHA256 为 `c90d82e73e22bc94fdc5fcecc1162b5e23ef3962b966c5c37a2ef963e12f91c6`，51,634,958 字节，显式 `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1`。干净应用补丁得到 1025 个相同源码文件，重新构建得到相同 ELF；不含 PT_INTERP，实际 pinned-FD 预检通过。仓库仅保存源码补丁和文本证据，二进制在忽略目录。其默认版本字符串 1.10.0 不是上游源码版本；固定上游 commit 仍为 `fc8c5a24b16991f98cd736950c17d1aa306a5041`。

首个普通构建继承了 CGO 设置，包含动态解释器。直接 exec 的原生测试通过，但产品 Apply 在创建操作前正确拒绝；该动态 SHA 的能力已撤回，原二进制、失败和旧批准文本保留。随后静态构建通过真实产品路径。另一次旧生命周期回归因沙箱私有目录检查失败，保留原件后在宿主按相同源码通过，未放宽路径检查。旧 candidate 收据和核心批准 artifact 保存各自当时范围，本次增量以新的产品机器记录为准。

当前编译器显式拒绝 Apps、GOST、启用 retention、进程/应用谓词及 IPv6 规则，后续任务补齐后才能声明支持。节点协议构造通过不等于七类远端握手已实测；完整计量、进程归属、连接 ready 延迟、其他平台和资源预算均未由这次验收获得批准。源码与证据按模块分批冻结，表内结果不是一次全仓库执行的合计。
