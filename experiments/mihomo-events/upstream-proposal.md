# T126：同源观测增强补丁提案

日期：2026-10-22。状态：保留当时供决定实验范围的提案；用户随后再次调用implement，已继续实施/构建隔离候选。
实际改动与限制以[manifest](candidate-manifest.json)和[验证记录](../../docs/validation/native-candidate.md)为准；提案条目不代表均已实现或验收。
基线为 [source.lock.json](source.lock.json) 的干净固定源码，不能在现有 dirty 二进制上做不明增量。
依据 [ADR 005](../../docs/adr/005-telemetry-replan.md)和[事件契约](../../specs/001-hoopa-proxy-manager/contracts/telemetry-events.md)，
保留进程、域名、终末、质量及规则完整性要求。

## 建议决定的实验范围

在私有 `/tmp/hoopa-mihomo-events-*` 目录，以上述固定 commit 实施、构建独立 Linux amd64 TCP 实验候选；
生成独立路径/版本标记/binary SHA/source commit/补丁摘要/依赖与能力记录，随后按任务执行受控回环探针。
默认不开启观测；仅明确选择的实验入站装配观测。未开启计量的 stock 路径维持独立，不读取观测目录。
不替换原核心，不安装服务，不修改父 shell/系统代理，不默认提升权限，不向上游提交或发行任何内容。
随后实施仅覆盖此隔离实验；长期维护或发布核心分支不在此次范围内。

## 文件与行为改动清单

以下是待实施的具体改动，不是对现有 API 的声称；最终补丁若超出这些职责范围，重新裁决。

| 上游位置 | 具体改动 | 必须保留的不变量 |
|---|---|---|
| `constant/metadata.go`、`adapter/inbound` 的 TCP 移交位置 | 附加私有 flow context，保存解析目标时的单调起点、原始 peer/local socket、入站与固定 generation | flow 在拨号前产生；原始 socket 与可变/声明 metadata 分开；普通 HTTP 多请求/复用不能按新业务域名复制隧道字节 |
| `component/process/process_linux.go` 及同包新增 Linux 证据文件 | 新增完整 tuple/inode 到 owner 集合的证据查询，输出 PID、comm/name_source、start ticks、boot/PID/net namespace、可验证路径、状态/reason | 不能复用“首个匹配 path”作为唯一证明；查询前后复核启动身份与持有关系；权限/共享/转交竞态无法排除则 unknown，不能冒用父/转发器 |
| `tunnel/tunnel.go` 的有序匹配入口、`rules/common` 的明确进程条件 | 将已发布的实例/精确名称/路径及 all 条件按原顺序求三值结果，必要时增加局部三值匹配接口 | 前面已确定匹配的规则优先；all 中确定 false 优先于 unknown；当前可能影响结果的 unknown 拒绝；能力不支持在启用时拒绝，不用永久拒绝全部流量冒充能力 |
| `tunnel/tunnel.go` 的 retry 回调 | 分配每次 attempt，记录开始/结果/失败阶段，在实际握手完成处记录 ready；实际 Conn 的链和 provider 复制为不可变事实 | DNS 在相应定义范围内；首次目标解析到 ready 的 flow 耗时与每次 attempt 耗时使用不同 definition，不把两者混算；失败无成功 duration，保留等待与原因 |
| `tunnel/statistic/tracker.go`、必要的已有计数 wrapper | 绑定原 flow/attempt；修正或绕开未经证明的 buffer 快路径，产生双向累计 checkpoint，转发双向结束后一次性取得 final，再从活动集合移除 | read/write 的 n、buffer partial/error、peek/lazy-handshake 归属有独立真值；不把 rejected/未写入字节当成功流字节；closed 是累计而非新增；Close/并发关闭不能重复终末 |
| 新增局部 `component/hoopatrace` 与显式实验初始化接线 | 单 writer 有界源日志、稳定序号/事件 ID、opened census、source replay/gap；独立 sender 对接 Hoopa 接收 journal | 不在逐连接路由时请求管理器或 SQL；源日志与接收日志分开；receiver 掉线不能让已发布路由失效；不引入通用事件总线或多内核层 |

进程证据第一范围限定同 UID、同网络命名空间、IPv4 loopback 的持有中 TCP socket。
这是候选支持范围，不能通过把预登记可识别样本事后改成 unknown 来通过 G01。
跨 UID、IPv6、FD 转移、后台后代等仍按矩阵取证；无法证明时保留阻断，不自动获取特权。
进程名称来源与 executable path 分开，Hoopa 精确名称规则不直接沿用 stock 的 EqualFold。

应用选路使用命名入站的规则上下文，并校验所有 listener 的 `proxy` / SpecialProxy 留空；
显式节点/组只能由本地规则序列确定。不能用入站直接指定代理来绕过更早的域名/IP/进程条件。

## 源日志、恢复与背压顺序

这里确定候选的顺序边界；具体帧/文件代码和故障测试仍须实现，接收 journal 的既有通过结果不转作源日志结果。

1. 核心启动核对私有目录和单 writer 锁，绑定 binary/hash/boot/PID/start/启动 nonce 的 source_instance/source_epoch。
   核心重启创建新 epoch，旧 epoch 保留可回放事实；采集器/管理器重启不得创建新的来源 epoch。
2. 新计量 flow 先为 opened、终末及故障标记预留有界日志容量，持久化 opened/census 后才能放行转发。
   预算同时限制磁盘、索引内存、活跃 flow 和单帧大小；无保留空间时拒绝新的完整计量，不影响既有基础入口。
3. source_seq/event_id 在来源单 writer 下分配；完整源帧写入与 Sync 成功后才视为已产生。
   写入结果不确定时停用新计量接纳，重开验证后按原事件身份消歧，不能猜测推进序号或自动截坏尾。
4. sender 重复发送来源已持久事件；接收端 Write+Sync 后才 ACK，ACK 丢失重送同 ID/原文。
   source_seq 只描述源日志，receipt_seq 只描述接收日志。当前计数 codec 不接受把 receipt_seq 塞入源帧。
5. 消费确认持久化后才能回收相应源日志段，确认丢失只允许重放，不允许提前删除。段替换/回收须 Sync 目录，
   恢复不依赖内存索引，保留有界 census 与缺口证据，未确认终末不能作为已消费清理。
6. 核心正常关闭由 final/census 收敛；异常退出后有 opened 无可信 final 的 flow 标记 pending/gap。
   周期累计只能恢复其覆盖边界内的累计，不能补造未观察字节、逐秒维度或业务 TTFB。
7. 对已有 flow，采集掉线继续原路径；日志/磁盘故障也不重新选路。预留的终末/健康信息不足时保留可恢复 census，
   下一次可读恢复须报告未知区间，不能用零或空库掩盖。新完整计量接纳保持失败，直到记录能力实际恢复。

源日志仅保存事件需要的标识、目标证据、计数和脱敏理由；不保存完整 argv/环境、请求正文、凭据或 URL query。
访问保护包括目录/文件、writer、sender/receiver 身份及读取范围；loopback 或共享 profile 不构成同用户安全隔离。
生产 SQLite 的检查点/增量/gap/version 事务仍留在 T019 与 P2 对应任务；本实验不预建生产双驱动或库表。

## 计时、路线和字节口径

- flow setup 从完整请求目标已知开始，包含规则触发的 DNS 和后续拨号；每次 attempt 另记本次 dial/handshake。
  不把二者共用一个 definition，也不把 tracker 创建时间当作起点。无法证明特定协议 ready 时，该能力不启用。
- 实际 route 只从相应 attempt 的真实 Conn 获取，保留 chain/provider 顺序与当时 generation/rule 的稳定映射。
  tracker UUID 与 flow/attempt 显式绑定；失败/重试记录不使用成功连接的路线，当前 group now 不回填历史。
- 首轮冻结 `mihomo_proxy_stream_v1` 时必须分别核对上传/下载、入口读取与成功写入、buffer/peek、部分失败和重试的归属。
  边界不同的量保留独立字段/定义，不能相加伪造同一流字节。旧 GOST 账继续使用原 boundary。
- 加密业务首字节与多路复用内每请求时延无证据则 N/A；不做 TLS 解密，不把通道建立成功当作业务成功。

## 许可、维护成本与验收出口

固定核心的 LICENSE 为 GNU GPL version 3。Hoopa 管理器保持外部进程集成，不链接 Mihomo；核心修改须保留上游许可、
版权与修改说明。此次提案不授权发行，传递依赖/发行义务仍在实际发行前单独核验。
同一 commit 与现有二进制 revision 一致不解决 dirty 状态，候选必须从固定干净源码记录自己的 patch/binary 身份。

改动涉及接入、Linux 归属、规则三值、计数快路径、计时与持久日志，不能声称已经证明是低成本“小补丁”。
维护工作包括每次上游升级重放补丁、buffer/handshake/route 语义差异检查、身份权限矩阵、崩溃/背压恢复及许可记录。
先限一个源码 commit/平台/TCP 范围；接口或代码范围扩大时由主代理裁决，不能用通用框架提前覆盖未来协议。
提案时没有补丁LOC、构建、资源或长期维护成本实测；后续构建及制品记录见manifest，资源/长期维护仍未实测。

取得实验授权后的顺序：

1. 实施限定增强及对应源端故障测试，生成可审查实际 diff；核对 stock 路径的未计量行为，构建到私有临时目录。
2. 记录完整 source/patch/binary/toolchain/能力清单；构建或能力不满足则 T126 继续未完成，不启动后续探针。
3. 可运行候选就绪后，预登记两同名进程、子孙、域名和 100 短流，串行执行 T127、T128、T129；权限不足明确未验证。
4. 复核 T012–T017 的同 run 证据，再通过 T015/T016 的生命周期/安全/回滚矩阵和 T018 评审。
   编译、codec 测试或接收重放通过均不能直接放行 P1。
