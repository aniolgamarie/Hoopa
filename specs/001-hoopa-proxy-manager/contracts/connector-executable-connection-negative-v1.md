# 组合核心的四元组与TFO负向边界 v1

2026-10-24，运行前登记，T132有限子步骤。固定001–012核心 `4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27`、组合对象 `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500`、20FD移交和6.18.54.1独占隔离guest。前置为同制品正向、五类归属边界及范围拒绝实测；旧0010 TFO/001–009 tuple通过不继承。本批不改核心或BPF。

每场独立VM、新世代：先完成source叶内普通HTTP→alpha.test/node:a基线流33/33；再执行指定负例；最后由正常source叶、初始namespace的独立进程创建新socket/不同tuple尝试普通connect。所有目标为127.0.0.1:18993。记录原PID/start、socket/netns cookie、实际bound tuple、native syscall参数和结果；三方oracle不参与准入。普通基线仍要求原v4 executable证据、真实线路/ready及精确终末。

| 场景 | 第二次调用与预期阶段 | 第三次普通连接与owner观察 |
| --- | --- | --- |
| `tuple-reuse` | 新PID/start/cookie，以SO_REUSEADDR及必要的有界TIME_WAIT等待重用基线四元组；实际connect应成功，但收到EOF/明确reset且客户端零协议/应用写。原accepted关联拒绝，不生成新flow，不冒用旧connector。 | 新cookie、不同tuple的普通connect返回EPERM；固定core观察预期admitted=1/rejected=1/revoked=true，控制状态degraded。该负例包含sockops故障与随后Owner.Admit拒绝，不将其简化成connect即时拒绝。 |
| `tfo-send` | source叶内新socket调用带MSG_FASTOPEN的sendto，使用固定无认证敏感信息的33B探测payload；预期EPERM且syscall未报告成功发送字节，目标零新增应用数据/flow。记录guest TFO sysctl和socket原始TCP_INFO诊断；不开额外监听旁路。 | 同世代正常新socket connect返回EPERM；owner仍admitted=1/rejected=0/revoked=false，因为拒绝发生在accepted之前。 |
| `tfo-connect` | source叶内新socket显式设置TCP_FASTOPEN_CONNECT=1并读取确认，再调用普通native connect；预期EPERM且零协议/应用写，不生成新flow。 | 同世代正常新socket connect返回EPERM；owner同上。 |

tuple基线和复用连接的实际四元组必须逐项相等，cookie和进程实例不同；新tuple followup必须与二者不同。若复用connect未成功到达所登记的关联拒绝阶段，保留该失败及errno，不按“也是拒绝”放行。客户端不发送协议头来试探是否可用；EOF/reset只在有界读操作中观察。对照旧tuple夹具时保留accepted_tcp_connections为当前gauge的定义，不能当累计次数等待。

TFO模式只在guest临时设置该实验所需的TCP_FASTOPEN开关，记录前值/设置值/读回值并在结束时恢复、核对；宿主不改。必须先核实固定内核支持该调用形态，ENOPROTOOPT或其他错误为未完成/失败，不能算作预期EPERM。tfo-send本轮要求cgroup gate早拒，和旧0010已进入TCP子连接的181B试验阶段不同。即使本批通过，也不声称实际执行了核心TCP_INFO的TFO_CHILD分支或获得TFO身份支持。

基线完整关闭后保存原来源文件前缀的长度与SHA256；负例和followup后该前缀逐字节不变。最终只有基线一个flow/完整7帧epoch，无gap、tail=0、双向33字节守恒，代理/目标记录只包含基线token。所有socket/wrapper有界清理，核心SIGTERM退出0；原始导出、镜像/核心/对象/decoder摘要及20FD CLOEXEC严格复核。三种拒绝的具体errno/阶段、owner状态和字节边界各自检查，不互相替代。

本轮以实际行为证明有限负向边界与同世代持续拒绝，不读取的BPF计数保持未观察。若任何阶段与上述预期不同，停止该矩阵并保存原run，分析新证据后另行登记修正；不覆盖旧原始材料。普通基线、负向调用和离线准备分别计数。正式identity/name/path能力仍false/unverified，G01新增0，T132/T137及P0/P1门槛保持；恢复/长时间回收、非native ABI及任意并发连续性另验。
