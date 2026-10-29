# T127 source5 独立边界验收

2026-10-25。已有100/100正向批次，以下边界另行登记，不增加、冲抵或重写其分母。每个边界保留准备、正式登记、单次执行、失败和独立复核，未运行不能写成通过。固定0017、内核/BPF、trusted-root guest及只影响自有VM的范围保持。

## PID复用

一场VM的两条原始连接使用同一数值PID=30000、同名/bin/python3，但有不同启动身份和socket cookie。父进程先确认目标PID不存在，再向guest初始PID namespace的ns_last_pid写29999，立即执行一次Popen；若实际PID不符则本次失败，不反复尝试获得满意结果。这个操作不发生在宿主。

两条连接分别使用HTTP/alpha.test/node:a和SOCKS5/beta.test/node:b，各33字节双向。每条仍须独立完成connect前原socket及活进程观察、truth fsync/release屏障。第一条必须完成终末、等待原客户端退出并回收、观察源帧closed33/33，且等待至少25ms后才能创建第二个实例；固定/proc时钟100Hz，实际start_ticks仍须严格增加。第二次创建前没有遗留fixture worker。

严格来源解码独立核对每条PID+启动身份+cookie+原程序；两个flow ID/call token分开，第一终末source_seq早于第二opened，不能仅凭同PID关联。先两条准备，再同冻结镜像新登记两条正式边界槽位；formal_g01_new_samples始终0，两个边界结果不属于100正向新增。

## 身份凭据缺失

拟在完成一条基线后，暂停全部自有核心线程，让新客户端在connect前登记并释放到“已connect、零代理协议字节”的第二道屏障。特权guest父进程按原始cookie定位实际exe_records条目，保存完整原记录，另开同map ID的可写FD，仅删除该key并核对ENOENT，再恢复核心。原始核心handoff FD仍为只读；pidfd复制不能改变其访问模式。

预期是保守拒绝入站、Owner rejected增加/revoked、状态degraded、没有该cookie的opened或转发线路。必须记录特权删除receipt、原始map身份与前后状态；status没有缺失原因字段，不能凭共享错误消息声称观察了具体内部分支。未admit连接没有来源账，不能把未记录字节写成来源终末0。该夹具只验证缺失记录的拒绝，不宣称一般用户能删除map或系统能抵御恶意特权者。具体ABI/运行检查须在执行前冻结；这里尚非运行结果。

## 权限与三值规则

权限负例使用独立guest子进程实际清除groups、五类capability、变更全部UID/GID到65534并设置no_new_privs，记录同进程/proc状态和初始user namespace。在既有unprivileged_bpf_disabled非零前提下直接调用有效BPF_MAP_CREATE，核验内核EPERM；不得改sysctl制造前提，不能用loader的geteuid拒绝替代内核调用。若返回了FD只关闭自己获得的FD并记失败。EPERM不单独证明具体LSM或sysctl分支，也不等于普通应用不能使用有权限的collector。

程序文本未知的三值边界须保持原始非法UTF8执行文件字节与source5 admission成功的证据，再核对影响规则判断的unknown拒绝和AND中false支配unknown、两种条件顺序一致。沿用T140已证明可行的真实输入，另加正式connect前登记；不把既有T140校准直接改名为本轮正式边界。完整G05规则/重载矩阵仍由T129承担。

全部边界完成之前，T127/G01仍未放行；完成此处也不等于T128/T129、P0或项目完成。


## 2026-10-26 三值矩阵执行前冻结

采用独立 `private/t127-source5-threevalue-vm/image-prepared-v1`，initramfs SHA256 `76576e73e7d3c513de093505dc4e9be7622ce5be7c7323f9e5afd27fdbe60fe6`；核心仍为0017。七类各HTTP CONNECT/SOCKS5两条，先完整登记14条准备槽位，全部通过后使用同镜像另登记14条正式槽位，不增加正向100分母。每阶段只能登记一次，每类只能启动一次；失败保留原始结果。

| 场景 | 原始程序文本与规则 | 预期 |
| --- | --- | --- |
| directory-name-known | 目录非法UTF8、basename python3有效；PROCESS-NAME python3 | node:a，33/33 |
| directory-path-unknown | 同一非法目录路径；PROCESS-PATH /bin/python3 | unknown拒绝，来源终末0/0 |
| basename-name-unknown | basename含非法UTF8；PROCESS-NAME python3 | unknown拒绝，来源终末0/0 |
| and-false-first / and-false-last | DOMAIN never.test为false，PATH为unknown，两种顺序 | AND为false，后续MATCH node:b，33/33 |
| and-true-first / and-true-last | DOMAIN alpha.test为true，PATH为unknown，两种顺序 | unknown拒绝，来源终末0/0 |

所有目标均为alpha.test。非法字节路径必须真实复制并执行，程序字节hash与原/bin/python3一致，device/inode与路径另存，原始路径字节与source5快照一致；不能用comm或人工改写事件代替。沿用原connect前独立PID/socket/proc登记及fsync/release屏障。

规则拒绝与上文缺凭据拒绝不同：本组已admit且须存在同flow的opened、setup_finished失败、route_decided REJECT、closed 0/0，无实际connection_ref及segment。客户端须已发送完整代理目标请求、没有应用字节，并观察实际协议拒绝/EOF/reset；timeout不能通过。空fixture accept只表示未观察到该夹具接受连接，不证明零SYN。共同的unknown_or_route_failure错误文本不能单独识别内部原因，结论依赖完整输入与原始事件链。准备和正式执行结果另记，此段不宣称已运行。
