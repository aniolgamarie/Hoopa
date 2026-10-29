# 原始 Admit 调用整段耗时的有限测量

2026-10-25 实际执行；准备工作为2026-10-24。按[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-admit-measurement-v1.md)，独立测量 VM、canonical 提取、host checker 均 exit0。机器记录（本地原件 `../../experiments/sourceidentity/admit-measurement-validation.json`，不随Git上传）保存88项离线准备、overlay包34项顶层/233项含子测试的race结果，以及1次VM、2条流、13帧和26份独立重算摘要的原始导出。这些计数分别记录，不相加。

这是专用 Go overlay 测量制品，不是新的正式14补丁候选。原001–013源树和二进制均保持：源树摘要 `fafda3d95776abda19a829eed49eaa063cf0e90b6418e2b451478375c03fa154`，原核心SHA `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`；本次再次只读重算一致。overlay只将原方法改名并添加同签名wrapper，调用原方法前取Go单调时间，原方法返回后立即计时；身份、错误、计数和所有权语义不改。新测量二进制SHA `b4b9f0dfc1bf05d8b58aa177b54c3580f215f4f9b51092e666f44f15862b1808`，镜像SHA `5c4096a4f033bd902368822457f0f5d3d399f3ecd7d8c6fb76e564d4623fbfc2`。

core.stderr原文恰有两条完整 `HOOPA_ADMIT_MEASUREMENT` JSON。每条均按原 accepted cookie，以及core/capture generation、原PID/TID、leader启动时间、client cookie，与真实v4 opened唯一对应；没有依日志先后关联，也没有在测量日志中制造flow_id。

| 协议 / 实际线路 | 原PID/TID | accepted / client cookie | 原Admit整段 ns | 精确换算 ms | 独立ready耗时 |
| --- | --- | --- | ---: | ---: | ---: |
| HTTP CONNECT / node:a | 83 / 83 | 4098 / 4097 | 161824 | 0.161824 | 12 ms |
| SOCKS5 / node:b | 84 / 84 | 3 / 2 | 89686 | 0.089686 | 31 ms |

计时scope为 `owner_admit_call_including_mutex_wait`，包括原mutex等待及同步socket、对象、故障、世代、map等读取；不包括accept前排队、此前内核callback，也不包括之后JSON序列化和stderr写入。整段时长不能称纯锁等待，不能与协议ready延迟互换。内部成本拆分和纯等待仍 unavailable；两样本不产生分位数、性能通过阈值或10分钟预算结论。

本场保持原family-concurrent判据：同名python3父PID83和自有Popen子PID84，各建独立TCP，child close_fds=true；两条成功ready的source_seq为4、9，均早于首closed11，之后closed12、epoch_closed13。两流原始296B exe记录与独立proc oracle/cookie/PID/TID/BOOT一致，路径 `/bin/python3`、dev2/inode8，call token分别1、3且consumed1。各发送33B后提前SHUT_WR，再收到精确33B与EOF；完整v4严格解码/重放，epoch complete、tail0、无gap。该屏障只证明连接重叠，不证明callback同时执行。

核心同PID接管20FD并恢复CLOEXEC；最终admitted2/rejected0/revoked=false，当前活连接gauge0，客户端均退出并回收，核心SIGTERM exit0。ready/end资源保留同PID/start的短窗口原文；原资源字段的 `admit_wait_latency=unavailable` 保留，不能把新增整段测量写成纯内部等待。

独立 scout 已完成实际材料审查，核对两条测量与原opened/oracle、13帧、26份导出及archive，未发现critical/high证据矛盾；审查文件及摘要见机器记录。

首次root overlay因包名与原hoopaid不一致而在包setup阶段失败，未产生VM证据；两个纯fixture准备错误涉及旧family的connected/ready/exchanged三行，修正后保留完整原oracle。失败记录均单列保留，不覆盖为通过；旧实验材料不变。

正式identity/name/path能力仍false/unverified，正式G01新样本0、旧0/100，T132/T137与P0/P1门槛不因本次计时自动完成。与[原生命周期记录](connector-executable-lifetimes.md)的运行结果分开计数。
