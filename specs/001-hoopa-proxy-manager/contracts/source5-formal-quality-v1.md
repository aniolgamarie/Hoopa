# Source5 受限正式质量与线路矩阵 v1

2026-10-26，T129执行输入。依据[ADR011](../../../docs/adr/011-source5-quality-acceptance.md)和六类已接受的[准备证据](../../../docs/validation/source5-quality-preparation.md)。本契约固定逻辑场景与接受方法；新formal镜像、所有具体slot/token/seed、运行命令和输入摘要仍须在第一个正式VM之前一次性登记。当前未执行正式矩阵，不改变T129/T014及P0/P1状态。

## 固定范围

- core0017 SHA256 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`，source5，原connect_initiator_v1身份证据；同hash的T127/T012与T128/T013接受记录为前提。
- Linux/amd64、固定6.18.54.1-microsoft-standard-WSL2 guest、初始namespace、受控专用cgroup/loopback；BPF/collector/libbpf/kernel/解码器沿用各已接受准备包的固定hash。每VM 2CPU/1GiB/150秒、无NIC、无宿主共享，guest root夹具不冒充生产降权部署。
- 只向独立新目录`private/t129-source5-formal-vm/<family>/`写正式登记和运行。原准备目录、失败、claims、摘要和镜像不可复用为正式执行；新的guest代码必须实际打入新cpio并完成全树/隔离导入/非Python依赖检查。
- 所有入口严格formal phase，拒绝preparation、重复/冲突phase标记与跨家庭slot。身份命名、generation域和claim独立；观测/身份/计量/时序谓词沿用已接受准备逻辑，不因正式化放宽。

## 六场、21个原应用槽

| 全局case_id | family | 原场景mode | 原应用槽 | 必验行为 |
| --- | --- | --- | ---: | --- |
| http-ready | http | held-opened | 2 | 两实际HTTP节点；未完整200前无setup；原flow/attempt/ref、33/33 |
| socks-ready | socks | held-opened | 2 | 两实际SOCKS节点；有效完整BND末字节前无setup；33/33 |
| direct-slow-dns | dns | direct-slow-dns | 2 | 每flow两次真实A/IN查询，首答扣留、第二答及时；flow包含规则DNS，attempt独立；33/33 |
| full-replacement | replacement | full-replacement | 4 | 旧A持有12B→checkpoint/receipt→drain→新B/A→旧A完成33B→旧core退出→新B；旧源不变 |
| upstream-failure | failure | upstream-failure | 7 | HTTP403、坏VER/REP/RSV、截断BND、HTTP/SOCKS停滞；明确closed 0/0，无Conn不推实际链 |
| nested-retry | nested | nested-retry | 4 | HTTP/SOCKS入站各经B(SOCKS)→A(HTTP)；另HTTP403后成功、SOCKS坏REP后成功各一 |

同名mode不构成全局身份；必须用case_id、family和内部mode共同绑定。每家庭slot使用`formal-<family>-NNN`且全矩阵唯一。registered、attempted、实际观察到的执行、passed、failed/pending分别保留，注册本身不等于执行。任何失败均保留原21槽分母和单次claim，不在原运行上改脚本重新判定。

`vm_started`单列，不将QEMU存在等同全部槽执行。`attempted`是在领取case一次性claim后计划尝试的槽数；`executed`是实际导出admission中观察到原客户端开始连接调用的唯一已登记槽数，核对slot/token/协议/目标和规范正数`connect_boot_before_ns`。它不保证TCP建立或完整身份证据通过。缺导出/缺记录保留未观察，重复、外来或矛盾记录不能静默去重算通过；始终满足`passed <= executed <= attempted <= registered`，passed只取完整host checker已证明结果。

每原应用连接必须有独立preconnect truth及fsync/release屏障、原PID/start/executable/raw ABI/socket cookie、同flow唯一来源关联。被测核心不得消费oracle。source/manifest、collector原journal、raw receipt以及source_seq/receipt_seq/delivery_index保持不同坐标；只接受固定严格decoder的实际结果。原source cutoff是完整原帧前缀，允许的末尾epoch marker按已有接收契约单列；缺marker交付不伪装为必然持久收据，`recording_status`和writer-end证据保持原义。

失败家庭的attempt数不是固定52：普通上游dial最多10次、受共享deadline约束；正式oracle按既定1–10次及失败/最后超时条件核对每flow。准备中观察到的50 failure + 2 timeout不能成为正式运行后的调参依据。无helper accept的最后deadline仅为严格受限允许分支；未触发就记未观察。嵌套两flow各1 attempt，成功重试两flow各恰2 attempts，失败的ready/duration/ref为空且不可被成功覆盖。

## 时间、路线与字节

各场冻结的hold、上限、deadline及取整容差进入正式输入，不能在看到运行结果后修改。源flow/attempt/ready/finish只在同一source时钟域比较；独立helper只比较自身持续时间。嵌套`partial_sent→release_authorized`两段hold各至少120ms，源outer ready需覆盖其和，按准备政策只允许2ms向下取整容差。不得把`partial_observed`换成`partial_sent`却仍沿用同一数值。

actual_connection_ref必须来自实际返回Conn，actual链与tracker binding属于同flow/attempt/generation；逻辑选择不是实际链。所有成功flow终末与唯一成功segment双向33B，失败家庭有真实来源终末0/0。嵌套内层relay包含外层HTTP握手头，按原始recv/send返回n和实际原文核对，不能要求每跳33B。checkpoint与terminal是累计快照，不能相加成重复账。

完整替换保留两个独立入口/端口、source/collector/cgroup和capture generation。要求两组BPF对象按类独立，旧detach后新owner仍接纳新的原connector。旧frames/manifest、12B checkpoint及receipt原文完整保留；新mapping的alias变化只作映射元数据核对，不能借其声称UI或同端口热切换已完成。

## 单列语义检查与接受

同固定源码的source5受控语义（本地原件 `../../../experiments/mihomo-events/source5-quality-injected-validation.json`，不随Git上传）、HTTP adapter/Pipe取消（本地原件 `../../../experiments/mihomo-events/source5-http-adapter-boundary-validation.json`，不随Git上传）及T128已接受的未知外层/预读/initial-write检查分别引用，不加入21真实连接或六VM分母。它们证明其明列输入层的分类/证明/计量不变量，不证明受保护核心存在客户端断开取消dial、TFO或网络lazy路径。

总matrix须绑定全部六份新registration、image、initramfs、harness与本契约SHA，然后才能逐场领取一次性执行claim。原始host观察保持task=false；独立原件复核后，由主代理另发受限接受决定。公共Go/Python入口只校验已接受原件、分母、关联和hash，不执行证据里的脚本或启动VM/核心/writer。真实入口验证必须显式启用且0跳过；默认缺输入的skip保持“未验证”。

T014消费本批原始质量/线路记录复核G03，T017/T018继续各自门槛。生产分位数、窗口联查、排序、CLI/API与TUI仍属于其后任务。本契约不提升未测协议、provider、平台或管理器功能的支持状态，不回写历史0/100、60/100和任何准备失败。
