# 同名父子连接与 PID 复用实测

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-lifetimes-v1.md)完成两个独立 Linux 6.18.54.1/KVM 场景。固定核心仍为 001–009、SHA256 `452effa120f0f0beea122169c9e442b6338389bc4768111c4ed7ffb8d5f44f9b`。104 项离线工具检查先通过；真实运行共 **2 VM / 4 流 / 26 帧**，本批无失败，原始文件摘要再次核对一致，详见机器记录（本地原件 `../../experiments/sourceidentity/core-lifetimes-validation.json`，不随Git上传）。

| 场景 | 原始实例与连接 | 实际线路、建立耗时和终末 |
| --- | --- | --- |
| 同名父进程 | PID 81，leader start 4394818411 ns，client/accepted cookie 2/3 | HTTP → node:a，11 ms，双向各 33 B |
| 同名子进程 | PID 83，PPID 81，leader start 4668825990 ns，cookie 4/5 | SOCKS5 → node:b，31 ms，双向各 33 B |
| PID 首次使用 | PID 30000，leader start 5100354408 ns，cookie 2/3 | HTTP → node:a，11 ms，双向各 33 B |
| PID 再次使用 | PID 30000，leader start 5443582829 ns，cookie 4/5 | SOCKS5 → node:b，32 ms，双向各 33 B |

两种入口最终均使用受控 HTTP 上游，表内耗时是同 flow 的已证上游 CONNECT ready，包含夹具预设延迟，不是业务 TTFB 或通用节点评分。所有 flow 的原 PID/start/cookie、BOOT 建连区间、原始端点、实际 connection/attempt、整数连接段和终末均由严格检查器核对；独立 node/target 按预登记 token 的完整负载验证线路，未只比较相同的字节数。

父子场景的私有运行是 `vm-family-concurrent-utqlky_y`。父进程以 `close_fds=True` 启动子进程，原 TCP 为 CLOEXEC，子进程独立建连。两个完整 ready 帧序号为 4 和 9，第一条 closed 为 11；只有同时观察到两条 ready 且尚未 closed，guest 才释放应用负载屏障，因而本批连接确实重叠。同名均为受控 `python3`，归属键仍是各自实例，未因名称相同合并原始事实。这不等于产品的同名聚合或排序界面已经实现。

PID 复用的私有运行是 `vm-pid-reuse-31tjbvzi`。仅在 guest 修改 `ns_last_pid`，两次唯一分配都实际得到预登记 PID 30000；首次正常终末可见、Popen wait/reap、旧 `/proc/30000` 消失后，第二次 spawn 比首次 reap 晚 26.66 ms。两流处于同一个核心/source epoch/capture generation，启动 ticks 和 BPF leader start 均不同，历史 flow 没有被当前 PID 回填。没有碰宿主 PID 分配设置，也没有分配失败后静默重试。

本批仅关闭两个有限验证子步骤。继续按[早退契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-earlyexit-v1.md)验证原 connector 消失而 writer 继续使用 FD，并处理名称并发一致性、namespace、tuple 复用、缺失/冲突及覆盖连续性。T132/T137 未勾选，139 项/21 完成，正式 G01 新样本 0、旧 0/100 和 P0/P1 门槛不变；其他平台、真实 SSH/注销/休眠和完整性能预算未验证。
