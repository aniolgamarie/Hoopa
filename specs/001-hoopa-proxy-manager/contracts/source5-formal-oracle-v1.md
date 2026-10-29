# T127 source5 正式原连接 oracle

2026-10-25。以T140的0017固定候选为前提；实施结果见[正式正向有限验收](../../../docs/validation/source5-formal-identity.md)。旧0/100及首轮60/100保持其失败记录。本文具体化既有T127/SC-008，不改变connect initiator归属或100正例门槛。

## 组成与前提

100个正例分成ordinary、descendant、fork-writer、scm-writer、thread五类，各20个逻辑槽位。每类独立VM/来源世代，使用同一冻结镜像、核心、BPF、内核及检查器。各类alpha.test/beta.test各10条，HTTP/SOCKS各10条；同名真实解释器形成独立进程实例。descendant由两级fork后的子孙自行connect；fork/SCM由原父connect、后续子进程写入，仍归原父；thread区分caller TID与TGID。PID复用、缺证据/权限、三值拒绝另列边界矩阵，不冲抵这100条正例。

正式登记前，先在同一冻结镜像/工具组合对五类各做两条准备校准，检查拓扑、namespace、cgroup、原exe及入口可见性。此10条不属正式分母；前提有变化先回T130。只有准备通过且独立复核后才新建不可覆盖的正式登记，固定全部100槽位、seed、派生token、协议、目标、字节数、线路、所有制品摘要及准备证据。每类只能认领一次执行；失败不得覆盖或重跑成同一run。登记后的启动失败、unknown、缺失、超时、检查失败都保留，不能据此减小分母。

## 每连接双阶段屏障

客户端先创建普通IPv4 TCP socket并bind原端口，读取socket/netns cookie、进程PID/TGID/TID、启动时刻、boot/namespace、原执行文件字节路径与inode/device，证明尚未connect；输出preconnect后等待。独立guest父进程在实际/proc重读该活进程及祖先，核对程序/启动身份/cgroup/命名空间，向独立append-only JSONL truth账本写完整观察并flush/fsync，保存该次返回时间，随后才能向客户端发精确slot/token/generation/cookie的release。

客户端核验release后调用connect，记录前后单调时间、返回及socket身份。父账本的fsync返回必须早于connect前时间；该账本是独立oracle，不能喂入BPF或作为resolver身份输入。guest使用内存文件系统，fsync表示本次运行内的登记顺序，**不承诺VM断电后的持久恢复**；host正式槽位登记另做真实磁盘fsync。父/子writer、FD继承/SCM转交、后续socket关闭均单列证据，不改原connector记录。

## 判定与账本

严格source5解码后按原socket cookie和完整启动身份关联，核对原始112/296字节证据、程序名/路径、目标、规则前绑定、实际节点/ready及33/33终末；legacy process继续unknown。独立node/target记录用唯一payload token关联，不能只按流数推断线路。每类输出全部登记槽位的逐项结论，缺失也有记录；聚合固定100分母，不能只读取成功host-check文件。完整通过还需要T127另列边界矩阵，100/100本身不等于T127或P0完成。

可信root夹具、固定Linux/amd64、初始namespace、专属cgroup、普通loopback TCP、无NIC/宿主共享目录的边界继续有效。此oracle不代表生产launcher、其他内核/平台、非特权部署或后续writer精确识别能力。
