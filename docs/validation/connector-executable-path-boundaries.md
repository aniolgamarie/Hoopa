# 可执行文件路径的实际边界

2026-10-24，173 项离线检查后，使用同一 BPF 对象完成一次隔离 VM `vm-executable-path-boundaries-m0_fk2mj`，六个新 socket 的结果均符合[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-path-boundaries-v1.md)。本批无实际失败，前一批主机检查器失败仍单独保留。见机器记录（本地原件 `../../experiments/sourceidentity/executable-path-boundaries-validation.json`，不随Git上传）。

| 阶段 | kfunc 返回 | 原始路径/错误 | connect 结果 |
| --- | --- | --- | --- |
| 初始 | 13 | `/bin/python3` | ECONNREFUSED |
| rename | 23 | `/bin/hoopa-exe-renamed` | ECONNREFUSED |
| 移入超长路径 | -36 | ENAMETOOLONG，缓冲不解释为文本 | EPERM |
| 移回短路径 | 23 | `/bin/hoopa-exe-renamed` | ECONNREFUSED |
| unlink | 33 | `/bin/hoopa-exe-renamed (deleted)` | ECONNREFUSED |
| 解除 LSM 后新 socket | 无记录 | 保留缺失 | EPERM |

测试目标没有 listener，ECONNREFUSED 表示准入允许后由 TCP 拒绝，并没有代理转发。前五次 cookie 为 1/2/3/4/5，最后为 4097；不是通过预期 cookie 连号判断正确性。五条记录中包含超长错误原文，之后所有读取均逐字节保持。成功路径返回长度包括 NUL，逐字节匹配独立 `/proc/self/exe` readlink；超长阶段保存完整独立路径，只允许错误返回，未从截断缓冲编造名称。

四次文件操作前后均记录参数、readlink 和 stat。整个过程中是同一个 dev/inode；unlink 后目标文件不存在，`/proc/self/exe` 仍可观察正在运行的文件对象。comm 始终不变，因此路径名称与内核 comm 的确是两个不同字段。删除标记保留，不将其剥除后冒充当前存在文件，也不能从字符串本身消除同名或后缀歧义。

累计状态最终为 captures=5、connects=6、allowed=4、refused=2、fault=0。配置 freeze、map 属性和 FD 权限、程序/link/BTF 关联及原顺序清理通过；三份 guest 导出文件重新核对摘要，独立证据通道与内核日志检查通过。本轮仅在即将销毁的 guest 内改名/删除 Python 二进制，未改变宿主文件。

结果说明带引用的文件仍能产生随路径变化的观察，不能被当成永久程序身份。它不证明并发 rename、mount namespace、exec、PR_SET_MM_EXE_FILE 或旁路连接。当前验证每次创建新 socket；后续[同 socket 重试反例](connector-executable-socket-reuse.md)已复现旧记录再次使用，并以[一次消费修复](connector-executable-consumption.md)关闭已消费记录重用。名称/路径能力与 T132/T137 不自动完成，139/21、正式新样本 0、旧 G01 0/100 及 P0/P1 门槛保持。
