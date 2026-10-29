# 组合核心的连接负例与后续拒绝

2026-10-24。[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-connection-negative-v1.md)的三个独立VM场景均完成VM、canonical extractor和host checker，三阶段均exit0。机器记录（本地原件 `../../experiments/sourceidentity/executable-connection-negative-validation.json`，不随Git上传）保存逐场原始值、导出摘要和有限边界。

本批为131项离线准备检查、3次实际VM、3条正常基线流、3次负向触发和3次正常后续尝试。来源共21帧，83个导出文件（27/28/28）逐项重新核对字节数和SHA256。负向尝试不计作成功转发流，离线检查不计作真实连接样本；本批没有实际VM失败。

| 场景 | 基线HTTP / node:a | 负向触发 | 同世代正常后续连接 | 触发后的owner状态 |
| --- | --- | --- | --- | --- |
| tuple-reuse | PID84，12ms，33/33B | PID87，原四元组native connect返回0，随后EOF，零协议/应用写入 | PID88，新socket、31003，`-1/EPERM` | `admitted=1,rejected=1,revoked=true`，`degraded` |
| tfo-send | PID84，11ms，33/33B | PID87，原生`sendto(MSG_FASTOPEN)`尝试33B dummy，`-1/EPERM`，接受0B | PID88，新socket、31003，`-1/EPERM` | `1/0/false`，`running` |
| tfo-connect | PID84，11ms，33/33B | PID87，TCP_FASTOPEN_CONNECT设置并读回1，native connect返回`-1/EPERM` | PID88，选项0、新socket、31003，`-1/EPERM` | `1/0/false`，`running` |

## 原连接与来源保持

三条基线均经HTTP入口到alpha.test/node:a，原client为PID/TID84；每条v4 opened的connector v2及296B executable记录与原cookie、PID/TID、非零call token1和BOOT区间相符。独立`/proc/self/exe`前后oracle为`/bin/python3`、dev2/inode8；basename为`python3`，与comm分开记录。核心没有读取这些oracle来准入，本轮也未独立转储内核map来替代核心携带的raw record。

基线发送33B后提前SHUT_WR，完整收到33B回复和EOF，实际ready、终末和段守恒均保留。每场最终只有这一条flow、7帧、完整epoch，恢复报告为closed、33/33B、tail=0，无gap。负向触发前保存的6帧closed prefix在触发及followup后字节与SHA256均不变；活动文件完整可见只是只读观察，不声明这次读取证明了fsync。

tuple场基线为PID84/start_ticks422/cookie2，触发为PID87/start_ticks578/cookie4，两者实际使用`127.0.0.1:31001 → 127.0.0.1:18993`。基线abortive close、退出并reap后，登记等待至少1.2秒；触发connect实际成功，随后只收到EOF，没有发送代理协议或应用数据。followup由另一进程创建cookie6和不同四元组，不重试原socket。

两项TFO场景只在受guard保护的guest内把tcp_fastopen从原文`"1\n"`临时设为1543（0x607），读回一致，finally恢复原文并再次读回。`tfo-send`实际调用为`sendto`，flags为0x20000000，33B dummy全量作为调用参数保存；返回EPERM、接受0B，没有回落普通connect。`tfo-connect`实际设置并读回选项30=1，然后执行原生blocking connect；不把不支持选项当通过。原始TCP_INFO仅作诊断，没有宣称本轮产生了TFO child或重做此前TCP_INFO覆盖实验。

独立node和target记录各只有基线33B token，负例没有第二条opened、路线或目标payload。三客户端均由自有Popen对象有界等待并reap，核心SIGTERM退出0。

## 状态与证据边界

tuple触发经过accepted准入后，owner拒绝数增加并撤销，运行状态成为degraded。两项TFO在syscall返回EPERM，owner仍为`1/0/false`、running；三场当前accepted连接gauge在阶段采样时均为0，它不是累计连接数。

三场的正常新socket followup也返回EPERM，证明本次采集世代后续连接仍被拒绝；本轮不宣称自动修复或生产恢复。`running`和`revoked=false`不能证明捕获健康，owner计数只覆盖accepted admission。本轮未读取原始BPF fault计数，也未识别唯一内部拒绝分支；尤其TFO send可能先因缺少native-connect调用token等条件被拒，不能据结果认定某一TFO判断先执行。没有观察到错误身份授予，也不把本结果写成旧错配的复现。

## 固定输入与保留记录

- [001–012候选](../../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)核心SHA256为`4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27`；组合对象为`1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`。
- 镜像41,978,248B，SHA256为`498bb700ed7e0a975c650ed47cba9c808fead8d432b38f79913323282170194b`。固定6.18.54.1内核、BTF和BPF LSM、20FD handoff及核心恢复CLOEXEC均核对；三场采集generation互异。
- VM没有NIC或宿主共享，不修改宿主BPF、cgroup或sysctl；UART保留内核日志，virtio独立传输证据。实际来源由v4严格解码和只读恢复工具校验。
- 独立scout完成三场稳定材料复核，83/83导出、prefix、阶段身份/errno、终末与sysctl恢复符合，未发现契约层面的证据矛盾。

准备阶段131项功能检查通过，但额外交付whitespace断言因继承的guest.py:396行尾空格返回1；该非功能检查未通过，冻结源码保留原样。随后文件hash、JSON和镜像一致性核对通过；不能把它概括为“所有检查全过”。旧0011正向失败和其他旧记录没有改写。

本批只是固定内核、固定制品上的三个有限负例，不覆盖非native ABI、并发连续性、hook缺失、崩溃恢复或长期map回收，也不证明跨平台生产可用性。正式identity/name/path仍false/unverified，PROCESS规则仍拒绝；G01新增正式样本0，旧0/100保留。T132/T137未完成，139项中21项完成，P0/P1未放行。前序[范围负例](connector-executable-scope-negative.md)与[正向五模式](connector-executable-core-edges.md)仍按各自范围解释。
