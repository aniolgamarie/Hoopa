# 原始 connect 内的可执行文件快照可行性

2026-10-24，T132 补充研究。当前 `comm` 含义及 unverified 状态不变，本实验不将可执行文件 basename 冒充内核 comm，也不提前改变产品默认名称/合并/规则语义。目标是验证固定内核已提供的引用和路径读取机制，能否为应用信息增加独立、明确来源的字段。

只在现有固定内核的独立无网卡/无宿主共享 VM 执行。检查实际 BTF 中 LSM socket_connect、bpf_get_task_exe_file、bpf_path_d_path、bpf_put_file 的存在及参数，读取 guest 启用的 LSM 列表。一个专用非 sleepable `lsm/socket_connect` 程序仅处理自有 TGID 的指定 loopback TCP 入口：保留前序 LSM 返回值，从 current task 取得带引用的 exe_file，通过内核路径 kfunc 写入有界缓冲，释放引用，按原 socket cookie 插入不可覆盖的值副本。读取失败、超长或插入冲突不能产生成功记录。

独立 cgroup/connect4 程序使用同一个原 socket cookie 核对有记录、同 TGID/TID、路径读取成功和无持续故障，再允许本次测试 connect。该 gate 用于验证关联与缺证据拒绝，不接入当前 Mihomo。配置初始化后 freeze，状态/记录禁止用户态修改，所有对象由同一 guest 进程持有。

预登记有限顺序：仅 attach gate 时，一次新 socket connect 应因缺快照被 EPERM 拒绝；随后 attach LSM，以两个新 socket 各进行一次无 listener 的 loopback connect，均到达 ECONNREFUSED，LSM 和 connect4 观察的 cookie 一致且两个 cookie 不同。在两次之间对自有进程改名，实际 comm 必须变化，但两次可执行文件路径应各自等于独立 `/proc/self/exe` 观察，来源明确为 exe_file/d_path。解除 LSM link、保留 gate 后，再用新的 socket connect，应再次因缺记录 EPERM；不能复用上一次记录放行。

保存原始 path 字节、返回长度、cookie、PID/TID、BOOTTIME、BTF 与程序/映射/link 证据，核对每个取得的文件引用均有释放路径；逆序解除自有 link、关闭 FD、恢复 comm。运行前先完成默认拒绝、路径错误/超长、cookie/身份冲突、阶段计数与清理检查。实际运行只证明该有限 kfunc/关联序列，不证明任意路径重命名竞态、io_uring、非当前 task、内核线程或完整生产部署。

若实际 verifier、LSM 启用状态、kfunc 或持引用路径语义不满足，保留失败并重新裁决，不能用裸指针读取或当前 `/proc` 回查替代所需快照。只有该补充原型的真实结果与来源限制清楚后，才评估产品名称来源；T132/T137 和正式 G01 能力不自动升级。
