# 可执行文件路径的改名、超长与删除边界

2026-10-24，T132 独立补充实验。沿用 executable snapshot spike 的同一 BPF 对象、实际 BTF、libbpf 与固定内核，不接入 Mihomo，不升级 comm、名称或路径正式能力。源码预期是 `exe_file` 引用保持文件对象有效，`d_path` 输出当前路径视图，无法放入 256B 缓冲时返回错误；文件引用不把路径字符串变成永久文件身份。

仅在独立无网卡/宿主共享的 VM，以唯一 PID1 Python 进程执行，模式为 `executable-path-boundaries`。先加载配置并 freeze，依次挂接 connect4 gate 与 LSM，均由该进程持有。保存每阶段 `/proc/self/exe` 原始 readlink 字节、原 socket cookie、PID/TID、comm、BOOTTIME、map 原始值和文件操作前后结果；文件操作只作用于本 VM 的正在执行 Python 二进制，不能触及宿主文件或其他工具。

六次新 socket、同一 127.0.0.1:18993 无 listener 入口，顺序固定：

1. 初始 `/bin/python3`，成功捕获，connect 到达 ECONNREFUSED。
2. 原文件 rename 为 `/bin/hoopa-exe-renamed`，同一 inode/dev 且 comm 不变；新快照等于新的独立 readlink，connect 到达 ECONNREFUSED，旧记录保持。
3. 把该文件 rename 至预先登记的绝对长路径（多个合法短组件、路径长度大于 255 且小于 1024，不使用挂载、symlink 或 chroot）。独立 readlink 确认全长路径、同一 inode/dev；kfunc 必须返回 `-ENAMETOOLONG`，gate 返回 EPERM，不把截断内容当成功路径。
4. rename 回 `/bin/hoopa-exe-renamed`；同一对象的新快照重新成功，connect 到达 ECONNREFUSED。错误记录不能伪装成零或消失，也不能覆盖旧成功记录。
5. unlink 此文件；路径不再存在，但 `/proc/self/exe` 仍可 stat 同一对象，独立 readlink 带 ` (deleted)` 标记。kfunc 返回的原始路径字节必须逐字节相等、connect 到达 ECONNREFUSED。此结果是有歧义的“已删除路径文本”，不是稳定应用身份，也不能去掉标记当成当前存在文件。
6. 解除 LSM link、保留 gate；新 cookie 无记录，connect EPERM，不以任何旧快照替代。

前五次各产生一个不可覆盖记录，第六次没有记录；cookie 六个互异。累计 `(captures,connects,allowed,refused,fault)` 依次为 `(1,1,1,0,0)`、`(2,2,2,0,0)`、`(3,3,2,1,0)`、`(4,4,3,1,0)`、`(5,5,4,1,0)`、`(5,6,4,2,0)`。长度错误记录保留原始 bytes 但不解释其 path 缓冲为文本；此前记录在之后所有阶段逐字节不变。map 属性与 FD 访问模式分别核对，不再将 BPF_F_RDONLY 当成保留在 map_info 中的对象属性。

运行前完成默认拒绝、状态/errno/路径/截断/同对象/缺记录/旧记录覆盖/清理篡改等离线检查，固定完整镜像/脚本摘要和预登记。实际执行由主代理完成，严格提取全部证据后用独立检查器复核；任何失败原样保留。文件删除仅发生于即将销毁的本 VM 镜像内，不尝试事后恢复为成功；link/FD 与自有 comm 的清理仍须验证。此有限顺序不证明并发 rename、mount namespace、PR_SET_MM_EXE_FILE、exec、io_uring 或 Fast Open；这些需要按实际用途追加验证或明确拒绝。
