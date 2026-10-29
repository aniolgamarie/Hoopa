# 采集挂钩的有限操作边界

2026-10-24，按[运行前契约](../../specs/001-hoopa-proxy-manager/contracts/connector-capture-link-operations-v1.md)完成 134 项离线准备和 1 次独立 VM。实际启动、原始材料提取、严格核验均 exit 0；机器记录（本地原件 `../../experiments/sourceidentity/capture-link-operations-validation.json`，不随Git上传）绑定镜像、固定 001–013 核心、组合 BPF、23 份原始导出和结果。没有运行宿主 BPF、修改宿主 cgroup/sysctl 或安全策略。

guest PID1 对自己持有的核心 PID77 成功调用 pidfd_open/getfd，取得三个 link 和三个匹配 program 的临时副本。先核对六个对象的 ID/type、program name、link→program 关联及 CLOEXEC，再执行以下操作；操作后再次读到同一对象信息。仅关闭六副本和 pidfd，各关闭一次且确认 EBADF；核心原 FD 保留。

| link | link ID | program ID | detach | update，匹配原 program、flags=0 |
| --- | ---: | ---: | --- | --- |
| call_enter | 2 | 10 | -1 / EOPNOTSUPP 95 | -1 / EINVAL 22 |
| call_exit | 1 | 11 | -1 / EOPNOTSUPP 95 | -1 / EINVAL 22 |
| capture_exe | 3 | 12 | -1 / EOPNOTSUPP 95 | -1 / EINVAL 22 |

操作前后 status 逐字段相同，admitted=1/rejected=0/revoked=false；capture_faults、exe fault bits 和两个 raw recursion miss 计数均 observed `"0"`，前后诊断相同。该 status 只反映已接纳连接，诊断是顺序读取，二者都不等于连续覆盖证明。

HTTP 流在操作前已完成并回收客户端；SOCKS5 流在全部操作及副本清理后才开始 connect。两流结果如下，均在发送 33 字节后提前 SHUT_WR，再收到精确回显和 EOF。

| 协议 | 原 PID/TID | 原 exe / token | 实际线路 | ready 建立耗时 | 精确终末 |
| --- | --- | --- | --- | --- | --- |
| HTTP CONNECT | 85 / 85 | /bin/python3 / 1 | node:a | 12 ms | 上传 33、下载 33 |
| SOCKS5 | 88 / 88 | /bin/python3 / 3 | node:b | 32 ms | 上传 33、下载 33 |

各 296B exe 原始记录与独立 oracle/cookie/调用 BOOT 区间一致；完整来源共 13 帧，两个 closed，epoch complete、tail=0、无 gap。最终 admitted=2/rejected=0/revoked=false，两个客户端 exit0/reap，核心 SIGTERM exit0。独立 scout 对 23 份导出逐一复核摘要，并检查 virtio 证据通道及对象/流关联，未发现 critical/high 矛盾。

本次没有成功解除或替换挂钩，只证明这组固定 API 调用被拒和之后普通流仍工作。raw tracepoint 的 probe info 请求只读回目标名长度，没有独立请求目标字符串缓冲区；原核心接管/准入另有确切 target 字符串核验，probe 核对的是同一被持有对象 ID 和 program 关联。实验期间 privileged harness 暂时增加了 BPF 引用，不是生产移交方案，也不证明可以抵御任意特权篡改。errno 与固定源码路径相符，但未插桩证明具体内核拒绝分支。

执行模型：主代理负责契约、真实 VM、裁决及最终验收；executor（gpt-6.1-sol / medium）准备夹具和离线检查，scout（gpt-6-luna / high）独立复核稳定输入与实际证据。首轮文档生成器误读 oracle 包装层而 exit1，已保留记录并只修正取值层级，原 VM/checker/材料未改动。

该子步骤完成；接下来按[新生命周期矩阵](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-lifetimes-v1.md)验证并发父子、PID 复用和退出先于接纳。名称/路径/正式身份能力仍 false/unverified；T132/T137 未勾选、139 项/21 完成、正式 G01 新样本 0、旧 0/100 和 P0/P1 门槛保持。
