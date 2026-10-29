# cgroup 挂钩实际移除后的有限拒绝验证

2026-10-24，按[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-cgroup-hook-loss-v1.md)，完成两个独立无网卡、无宿主共享 VM。每次 VM、canonical 提取、host checker 均 exit 0。机器记录（本地原件 `../../experiments/sourceidentity/cgroup-hook-loss-validation.json`，不随Git上传）绑定固定 001–013 核心、组合 BPF、镜像及 56 份逐一重算 size/SHA-256 的原始导出。本次共两条正常基线流、两次负向 connect、14 帧；95 项本包离线检查与 root 的 73 项 probe 检查分别计数。

每场先完成 HTTP CONNECT → alpha.test → node:a：原 PID/TID84，独立 socket cookie 与 296B exe 记录绑定，call token1、consumed1，原路径 `/bin/python3`，独立 proc oracle 的 dev2/inode8 前后不变。两场 ready 都为 11 ms；发送 33B 后提前 SHUT_WR，再收精确 33B 回显及 EOF，客户端 exit0/reap。保存完整六帧原始 prefix 后才操作挂钩。

| 模式 | 被移除 link | 核心 PID / 原 FD | link ID / program ID / attach type | BPF_LINK_DETACH | trigger connect / 接收 | 最后 capture_faults |
| --- | --- | --- | --- | --- | --- | --- |
| detach-sockops | hoopa_lifecycle | 76 / 219 | 5 / 8 / 3 | 0 / errno0 | 0 / EOF，无回应 | observed `"0"` |
| detach-connect4 | hoopa_connect | 77 / 218 | 4 / 6 / 10 | 0 / errno0 | 0 / EOF，无回应 | observed `"2"` |

操作仅发生在 guest PID1 自己持有的活核心上。pidfd_open(434) 返回 FD6、pidfd_getfd(438) 返回 link 副本 FD7，errno 均0；两者 CLOEXEC。对副本读取的原始 64B link info 表明 type3、link/program/attach type 前后一致，cgroup_id 从1变0。detach 使用 syscall321、command34、4B attr `07000000`，只执行一次。原核心 FD 的确切 ID、CLOEXEC 和 fdinfo 前后对应同一对象；没有对原 FD 额外请求 BPF info。最后只关闭副本 FD7 和 pidfd FD6，各一次并立即确认 EBADF，核心原 FD 保留。临时副本增加了 BPF 引用，不是生产移交方案。

卸载之后，两个独立 trigger 都为原 PID87、cookie4，从新端口31002向原入口127.0.0.1:18993执行普通 blocking libc.connect，SO_SNDTIMEO 为3秒，返回0/errno0；未发送 HTTP/SOCKS 或应用数据，随后实际收到 EOF。没有将超时算作通过。目标和节点只有基线 token 的33/33B，没有第二次连接或第二 token 数据。

status 从 ready 的0/0/false，经基线后的1/0/false，变为 trigger 后 admitted1/rejected1/revoked=true、state=degraded；live TCP gauge 均0，不能当累计接纳数。四次诊断按顺序读取，均 incomplete=false；sockops 场四计数始终 observed0，connect4 场仅 trigger 后 capture_faults 实测2，另外三个计数仍0。这个值不是普遍固定期望，也不是 sockops 返回值直接拒绝 TCP 的证据。

两场六帧 prefix 原字节保持，trigger 后没有新增帧；核心正常 SIGTERM exit0 后，各来源只有原基线 flow 的完整7帧，v4 严格解码/重放、epoch complete、closed33/33、tail0、无 gap。活动文件可见性仅为只读观察，不单独证明 fsync。

独立 scout 已完成两次运行的实际材料复核，逐场核对28/28导出、archive、detach/原FD/cleanup、零写EOF、计数及prefix，未发现重大证据矛盾；审查文件及摘要列于机器记录。

首次离线镜像构建因把 host-only checker 误列入 guest pins 而 exit1，日志及未完成 image-v1 保留；修正清单并增加复制目标一致性检查后，以新目录 image-v2 构建成功。没有改判旧失败或重跑覆盖旧材料。

本次证明整条 cgroup link 显式移除后的有限拒绝，不能外推为只漏一次 active 回调、所有内核路径连续覆盖、任意特权对抗或恢复可用性；外部现象没有识别具体内部 map lookup 拒绝分支。对照[raw/LSM 操作被拒的记录](connector-capture-link-operations.md)，这里是真正卸载。身份实例、名称、路径能力仍 false/unverified；正式 G01 新样本0、旧0/100，T132/T137 未完成，原任务与 P0/P1 门槛不变。
