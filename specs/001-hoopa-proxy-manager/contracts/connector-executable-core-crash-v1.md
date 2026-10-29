# executable-online 核心活动流崩溃与重开 v1

2026-10-24，运行前登记。固定 001–013 核心 `476d57884b810e96d8b44367afbf91001bc63b6b516930a66b89d6e8dc05467b`、组合 BPF `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`、隔离 6.18.54.1/amd64 guest；没有网卡或宿主共享。此处将[旧 v3 活动流崩溃判据](connector-core-crash-v1.md)迁移到 v4/20FD 新制品，不继承旧运行的通过结论。

1. 一个原生 HTTP CONNECT 客户端通过 source 叶内普通 connect 建流，核对原 PID/TID、OS 启动实例、socket cookie、296B executable 原始记录、call token、同调用 BOOT 区间及独立 exe oracle。真实线路 node:a，先发送并回显 12 字节，保持连接。
2. 运行器只读等待该 flow 完整且摘要正确的 `bytes_checkpoint`，上、下行均为 12，无 closed。有界等待，不能用应用回显替代来源帧；活动文件可见性不等于独立 fsync 证明。
3. 仅 SIGKILL 运行器自己持有的核心子进程，wait 确认 -9；随后旧客户端必须观察 EOF/reset 等明确断流并非零退出，不将普通超时当通过。保存其原进程/exe/socket 证据和异常，禁止伪造 final。
4. 旧客户端退出并被 reap、source 叶为空后，新 bootstrap 输出目录创建全新的 BPF 资源及核心，重开同一来源目录。两捕获 generation 预先登记且互异；两核心 PID、来源 epoch、9 个 map/6 个 program/5 个 link 的实际 ID 分代检查，20FD 移交及 CLOEXEC 均验证。配置 generation 可以保持不变。没有把旧 map/token 或 raw record 移交给新核心。
5. 新 SOCKS5 流到 node:b 完成 33/33 字节、真实 ready、精确 closed 和完整新 epoch。新 flow 的 connector/executable 绑定其实际后端世代，旧 opened 中的身份保持不可变。只读恢复旧流是 pending、最后可信检查点 12/12、final=null、tail=null；新流 closed、final=33/33、tail=0。
6. 旧 manifest/frames 在重开前、后逐字相同；来源 writer 元数据与实际原核心 PID/boot/binary 对应。严格 v4 decoder 与恢复工具固定摘要；代理/目标记录分 token 守恒。实际调用及端点、两节点 ready 不由预期配置填充。

核心内部 `scanPrevious` 的 gap 仍未通过受保护接口导出。本次不得向只读工具人工注入 writer 结束证明，也不得把其 pending 说成已经收到实际 API gap。旧测试专用 overlay 结果不继承；如为新 v4 材料另外运行内部恢复测试，需新输入、独立记录，且与真实 VM 外部事实分开。

本次最多证明有限的新组合 crash/reopen 边界；不是管理器退出继续转发、自动重启服务、断电持久性或跨平台保证。失败及重试独立保留，不改旧记录、正式 G01 0/100；正式新增样本 0，T132/T137 仍须满足其他完整条件。
