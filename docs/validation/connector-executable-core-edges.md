# Executable 组合核心：五种原连接归属边界

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-core-edges-v1.md)，固定 001–012 核心与组合 BPF 完成 **5 次实际 VM、10 条流、65 帧**的有限验证。每个模式单独启动 VM 和资源世代，两条流分别为 HTTP CONNECT→alpha.test/node:a、SOCKS5→beta.test/node:b。本批 5 次运行的 VM、canonical extractor、host checker 均退出 0，没有新增实际 VM 失败。191 项离线准备检查另行计数，不能当作实际流或正式样本。

完整字段、原始导出摘要及私有证据索引见 executable-core-edges-validation.json（本地原件 `../../experiments/sourceidentity/executable-core-edges-validation.json`，不随Git上传），固定制品链见[候选 manifest](../../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)。主代理负责实际执行；本文只读复核既有材料，没有重跑 VM、测试、提取器或 checker。独立 scout 运行证据审查已完成，未发现这五个有限模式的 critical/high 证据矛盾，并核对全部 105 份导出及十份原始 executable/oracle 绑定。

| 模式 | HTTP 原 PID / TID / writer；ready | SOCKS5 原 PID / TID / writer；ready | 原始归属观察 |
| --- | --- | --- | --- |
| thread | 86 / 87 / 86；11 ms | 90 / 91 / 90；31 ms | worker TID 与 leader 不同，opened 与 exe 原记录均绑定实际 caller TID |
| fork-writer | 85 / 85 / 86；10 ms | 89 / 89 / 90；31 ms | 子进程写入，opened 保留父进程 connect 身份 |
| scm-writer | 85 / 85 / 86；11 ms | 89 / 89 / 90；31 ms | 传递后写入，原 socket 与父进程归属保持 |
| rename-after-connect | 85 / 85 / 85；12 ms | 88 / 88 / 88；31 ms | connect 时 comm 为 hoopa-before，之后为 hoopa-after |
| exec-fd | 84 / 84 / 84；11 ms | 87 / 87 / 87；31 ms | exec 后 PID/start/socket 保持，原 executable 与 resume executable 分别保留 |

五个运行目录依次为 `vm-thread-om2a_cmr`、`vm-fork-writer-d2b3qgt4`、`vm-scm-writer-59nvgtkr`、`vm-rename-after-connect-2z01wszp`、`vm-exec-fd-dkxoa_1a`，均在 `private/t132-executable-core-edges/`。每个 VM 内的两条流具有不同 cookie 和 call token（本批各为 1、3）；不同 VM 的编号不能跨来源世代合并。

十条 opened 都是 source v4、connector schema `sourceidentity-core-admission-v2`，完整保留 296B executable 原记录。逐条解码得到的 cookie、TGID、caller TID、非零 token、BOOT ns 与原 connector 和客户端一致，consumed 均为 1；路径为 `/bin/python3`，basename 为 `python3`。路径首字节、返回长度/NUL 与独立 UTF-8 展示遵循 [v4 契约](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v4.md)，记录未解释的尾字节仍保留在原帧中，摘要可供复核。

原 connect 前后均由发起方独立读取 `/proc/self/exe` 原字节与 stat；两次都是 device 2、inode 8，路径与 basename 相同。thread 的采样发生在实际 worker 中；fork/SCM 的原 oracle 在转交前固定；exec 的原 oracle 在替换镜像前写入。exe BOOT 采样位于客户端原 connect 包围区间，且满足 `leader_start ≤ exe_boot ≤ connect_boot`；start ticks、socket/netns cookie、端点和世代分别核对。setup quality 的单调时间与身份 BOOT 坐标没有混用。这些 oracle 只供事后核验，不进入核心准入或选路。

SCM 两流都记录子进程在 recvmsg 前关闭继承的原 TCP FD，并在接收前观察到 EBADF；收到 FD 的 cookie、netns cookie 和两端 endpoint 与父进程原 socket 一致。描述符数字允许复用，结论不依赖“新旧 FD 数字不同”。单有 writer PID 不同不足以证明 SCM 转交，本轮同时保留这些接收顺序和 socket 证据。

rename-after-connect 只通过 PR_SET_NAME 修改 comm，未对可执行文件路径做 rename。两条流的 comm 从 `hoopa-before` 变为 `hoopa-after`，原 executable 仍为 `/bin/python3`、device 2/inode 8；这不是 comm 原子性或并发文件改名实验。

exec-fd 则真正执行 `/work/python3-resume`：它是 `/bin/python3` 的独立 regular-file 副本，逐字节相同，但路径与 inode 不同。实际 guest readiness 记录两个文件 SHA256 都为 `22e8301a0f29619bdcf7f60979efdccaaa995c3699a3f8cb69e0b6f06940e0c2`；原文件 inode 8，resume 文件 inode 775，device 都为 2。

两条 exec 流的 PID 分别仍为 84、87，start ticks、原 socket cookie/端点保持；resume 恢复原 FD 的 CLOEXEC 并核对 socket 身份。单列的 `executable_resume` 位于原 connect 之后，路径为 `/work/python3-resume`、inode 775，resume comm 为 `python3-resume`。原 `executable_before/after` 与私有 connector oracle 没有被覆盖，opened 仍保存 `/bin/python3`、inode 8 对应的建连快照。相同解释器字节不意味着没有 exec，也不能据当前 resume 路径回填历史。

每个 VM 的原帧都具有完整 13 帧 epoch：两条同 flow 的 opened、实际线路、connection binding、成功 ready、精确 closed，最后 complete epoch_closed，没有 gap。HTTP 和 SOCKS5 分别映射到单 hop 的 node:a、node:b，adapter type 均为 Http；ready、binding 和终末保持同 attempt/actual_connection_ref。十条流全部双向 33B、连接段守恒。独立 node 和 target 的实际 payload 记录逐流绑定各自预登记 token；公开材料只保存必要摘要，不复制认证配置或认证报文。

普通流沿用已冻结 helper 的有界 33B echo/EOF 顺序，本批不另作 early-half-close 覆盖声明；同制品的专门提前半关闭正向结果见[组合核心正向记录](connector-executable-core-positive.md)。新的 v4 只读 decoder/state 和 recovery 工具完成原帧验证与重放；每个 epoch 恢复出两条 closed、各 tail_bytes 0。本文另行复核全部 65 帧的 len/raw/SHA，并与保存的严格解码结果相等。身份仍来自 opened 原帧；RecoveryFlow 没有新增 executable 身份展示。

各运行都由核心接管同 PID exec 移交的 9 maps、6 programs、5 links，共 20 个不同 FD；ready 时这些已登记 FD 均已恢复 CLOEXEC。当前 live TCP gauge 在 ready、第一流后、第二流后均为 0，累计 admitted 为 0、1、2，rejected 为 0、revoked 为 false。每个核心都经 SIGTERM 退出 0，两个自有客户端退出 0、被 wait/reap，随后原客户端 `/proc` 项不存在。没有把 live gauge 当累计连接数，也没有扫描瞬时普通 FD。

| 固定制品 | SHA256 |
| --- | --- |
| 001–012 核心 | `4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27` |
| 组合 BPF 对象 | `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500` |
| 本批共用镜像（41,983,798B） | `ddfb9147a937bcf2d50e7783563d5b7e6fd4affdb4cdb51cca6e7392b59fc518` |

5 个 VM 的启动登记、镜像和导出 pin 相符，均使用固定 6.18.54.1 内核及原 libbpf/QEMU。每个运行导出 21 个文件，共 105 个，长度和 SHA256 全部独立重验；归档与专用 `/dev/hvc0` envelope 一致。boot.log 单独保留诊断，未发现 BUG/lockup/Oops/panic；没有网卡或宿主目录共享，也没有修改宿主 BPF、cgroup 或 sysctl。

准备阶段保留两项已修问题：新 checker 的 exact 原快照字段表初始漏列三个既有 capability/name 字段，补齐后才通过；复制 client 的初始 host help 找不到 helper，显式选择只读 helper 目录后通过。它们不计为实际 VM 失败。此前[0011 正向首轮的线路证据失败](connector-executable-core-positive.md)与旧候选、旧 manifest、原始材料也继续保留，不被本批成功改判。

本轮仅验证五种固定操作各两个受控普通流，不证明任意线程/FD 共享时序、路径 rename/unlink 并发、PID/tuple 复用、namespace、TFO 拒绝、缺钩子、连续覆盖、全 hook 排空、RCU 结束、长期回收或生产部署。名称、路径和实例能力仍为 false/unverified，legacy process/process_path 保持 unknown，PROCESS 规则不开放。T132/T137 不完成，总计 139 项/21 完成、正式 G01 新增 0、旧 0/100 均保持。
