# 固定 source5 正式质量与线路验收

2026-10-26。六场新登记的实际运行均已通过原始host检查：21 registered、21 attempted、21 executed、21 passed，failed/pending和未观察槽均为0。241个规范导出文件已核对，六个QEMU进程已退出回收。独立原件复核与公共只读入口验收均已完成，T129及消费同run的T014完成；P0/P1未放行。

固定核心为0017，SHA256 `5693d4ac2bda5fcae7e5ac037d23db1b2a48049f681b8c825b8b4db12ac1b967`。同hash的[身份验收](source5-formal-identity.md)与[来源/接收恢复验收](source5-formal-recovery.md)继续作为前提。[ADR011](../adr/011-source5-quality-acceptance.md)和[正式质量契约](../../specs/001-hoopa-proxy-manager/contracts/source5-formal-quality-v1.md)区分真实网络、受控来源语义与适配器边界，各自保留分母。

正式机器记录（本地原件 `../../experiments/mihomo-events/source5-quality-formal-validation.json`，不随Git上传）SHA256 `82f2810d05fd3fee69aa7cc71a95ca7468058e80b8b9cf814ad3cc03900fbbdd`；[受限接受记录](../../experiments/mihomo-events/source5-quality-acceptance.json)SHA256 `ce1b0afdfe5622bae78b47fe0889c1547040a06efdb19de18d6f5344ead4464c`。最终只读验收（本地原件 `../../experiments/mihomo-events/source5-quality-gate-validation.json`，不随Git上传）核验552个文件，Go固定工具链race运行1通过、0失败、0跳过；新runtime样本0。

## 新登记与实际执行

六个新formal-v2镜像及全部新slot/token/seed在第一台VM之前完成总登记。matrix SHA256为`7b9e2e7c84160b9ad3703e95147b54536f892fc7e7de61f80f64b1074d8ebc28`。总执行入口每场前复核六份registration、image/initramfs、harness与契约；matrix seal、外层串行锁、不可覆盖admission及family一次性claim共同保留执行顺序。HTTP与SOCKS虽共享内部mode `held-opened`，全局case、family及slot命名分别绑定。family runner是内部实验工具，完整矩阵准入由实际使用的外层入口执行，不宣称它是抵御恶意同UID绕行的安全边界。

`attempted`表示领取一次性case claim后计划尝试的原应用槽；`executed`只计实际导出中能核对slot/token/协议/目标及规范正数connect起点的唯一原客户端。QEMU PID另计`vm_started`，不能代替客户端执行。下表四个计数分别列出，不以成功数反推分母。

| 全局场景 | 登记 | 尝试 | 已观察执行 | 通过 | 规范导出 | VM耗时 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| HTTP完整200边界 | 2 | 2 | 2 | 2 | 39 | 11.698s |
| SOCKS完整BND边界 | 2 | 2 | 2 | 2 | 39 | 11.639s |
| DIRECT慢DNS | 2 | 2 | 2 | 2 | 40 | 10.335s |
| 独立核心/端口完整替换 | 4 | 4 | 4 | 4 | 50 | 13.145s |
| 上游拒绝/畸形/超时 | 7 | 7 | 7 | 7 | 38 | 36.001s |
| 嵌套链及首次失败后重试 | 4 | 4 | 4 | 4 | 35 | 11.698s |

每台VM使用固定Linux 6.18.54.1-microsoft-standard-WSL2/amd64、2CPU/1GiB、150秒上限，无NIC或宿主共享。BPF、cgroup、DNS及测试连接均在guest内，宿主DNS/内核/系统代理未改变。时间仅用于运行记录，不构成性能预算达标结论。

## 已观察的质量语义

HTTP两条实际node:a/b的attempt建立耗时为137/126ms，独立上游扣留最后回复边界124113335/124924755ns；SOCKS为138/124ms，完整BND最后字节扣留124037986/122295925ns。对应flow建立耗时分别是HTTP138/127ms、SOCKS140/125ms，不能与attempt混称。每槽两份原来源快照均未提前出现setup；释放后同flow/attempt的实际Conn引用和线路成立，双向终末及唯一成功segment各33B。SOCKS回复中的脚本BND地址不能充当实际目标socket证据。

DIRECT每槽有两次真实A/IN查询及TTL0回复。首查询到回复132836314/132231422ns；source中进入attempt之前为136580291/134038790ns，flow ready为137/134ms，而DIRECT TCP attempt的整数毫秒耗时均为0。0是小于1ms的取整结果，有实际ready证明；它不代表缺测。慢规则DNS属于flow等待，不被挪入较晚才开始的attempt。不同计时域只比较各自持续时间，不能直接减两个不同原点的时间戳。

完整替换保留独立旧/新核心、入口端口、来源及捕获世代：旧A先12B checkpoint/receipt并drain，新B/A连接完成后旧A续到33B；旧核心退出后新B再次成功。展示别名变化保存在mapping中，旧原连接线路保留。这个场景不证明同端口无缝切换，也不代表生产管理器已经实现接管和回滚。

七类失败覆盖HTTP403、SOCKS坏VER/REP/RSV、截断BND及HTTP/SOCKS停答。客户端写入入口的33B与实际代理转发字节分开：本次每条来源均明确closed 0/0，没有返回Conn的attempt不补actual_connection_ref或实际链。正式观察为52 attempts对应52真实helper accept：前五类各10次failure，两个stall各1次timeout，等待为5000/5001ms。未触发无helper accept的最后deadline分支；源码原超时/重试条件保持，这个52是实际结果，不是修改运行分母的目标。

两条嵌套连接实际经SOCKS B→HTTP A，另两条分别在HTTP403或SOCKS错误后第二次成功。失败attempt保留在原flow内，不能被成功覆盖；内层relay包含外层HTTP握手原文，与应用层33B不同。正式4 flow保留6 attempts、8 upstream accepts及4 target accepts；两个成功多跳attempt均256ms。两段partial_sent→release_authorized扣留分别125966922+124656056ns、125309048+125183077ns，各段均超过120ms。内层实际154/92B分别为121B请求头+33B、59B响应头+33B；原source/app仍33/33。

## 单列检查与能力边界

- 来源语义检查（本地原件 `../../experiments/mihomo-events/source5-quality-injected-validation.json`，不随Git上传）：同固定源码、race 2顶层/含子项11通过，0失败/跳过。身份/Conn/error/n明确注入，覆盖cancelled/unknown、已证ready不被后续Finish覆盖、partial段及并发checkpoint守恒；不加入21个实际连接。
- HTTP adapter检查（本地原件 `../../experiments/mihomo-events/source5-http-adapter-boundary-validation.json`，不随Git上传）：实际adapter加net.Pipe/注入dialer、显式context取消，race 1顶层/含子项5通过，0失败/跳过；不证明客户端断开会取消完整核心的dial。
- 固定边界检查（本地原件 `../../experiments/mihomo-events/source5-fault-boundaries-preparation-validation.json`，不随Git上传）：复用已接受的未知外层、预读、initial-write等15项检查，不因引用再次增加运行样本。

归属口径仍是connect_initiator_v1，PID必须连同OS启动实例及同次exe名称/路径快照理解；FD共享/转交不改写历史。原source_seq、独立receipt_seq和delivery_index分开；checkpoint与terminal是累计快照，不能相加。来源可见前缀及receiver ACK不证明SQL提交，严格恢复器的recording_status/writer-end原义保留。

当前profile没有获得客户端断开取消dial、真实lazy/TFO、同core在线selector写入、同端口无缝替换、命名provider、TLS/认证协议扩展或完整UDP/QUIC能力。平台范围限固定Linux实验；生产权限、SSH/休眠、生命周期故障矩阵、SQL账本及CLI/TUI另验。p50/p95、单位时间流量、进程/域名分组排序和窗口联查仍由T095–T103等后续任务交付。

七份epoch合计194条原source与194条接收journal/receipt原文一致，其中HTTP/SOCKS/DNS各13、完整替换旧9/新19、失败99、nested28；21个source flow均有终末。实际检查仍保留recording_status=unverified及writer_end_evidence=null。

原GOST身份0/100、首次正式身份60/100、准备镜像和合成探针失败均保留；没有将准备结果改名为正式通过。独立审查在冻结源和实际原件上执行，主代理另作接受裁决；公共入口只读校验已接受证据，既不再次启动核心，也不增加原始样本数。
