# 原生 connect 调用编号与可执行文件快照：有限修复对照

2026-10-24，按[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-call-token-v1.md)完成独立无网 VM 的有限阶段矩阵。机器可读摘要见 executable-call-token-validation.json（本地原件 `../../experiments/sourceidentity/executable-call-token-validation.json`，不随Git上传）。本记录只说明该固定对象在单线程、原生 x86_64 调用中的观察结果；身份能力仍为 `unverified`，没有接入 Mihomo 核心。

主代理完成 38 项离线准备检查、构建、实际 VM、canonical 提取（退出 0）及 host checker（退出 0）。运行目录为 `private/t132-executable-call-token/vm-executable-call-token-8brg0jqh`，QEMU 退出 0，用时 12.098 秒，guest 与 runner 的 `errors` 均为空。本文编写阶段只读复核既有原始材料，没有重跑实验。

对象 SHA256 为 `968f7c18c13a99b26ec699a15f7bbf8b6147a09e039549f6daec035784682730`；源码 SHA256 为 `654619b788c03fde90e227a227ed17470451f9b06060bad8f637eb17f6a8b7b9`。当前编译版本使用最多 8 次 CAS 的饱和编号分配，旧 compile-v1 保留。内核为固定 6.18.54.1，导出 BTF 为 6,018,902 字节，SHA256 为 `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9`。

实验先在挂接 BPF 前建立真实 TCP：client cookie 1 与 accepted cookie 2 均为 ESTABLISHED，应用字节为 0；随后关闭 listener，保留两端 socket。挂接后的阶段如下，errno 均来自原始记录。

| 阶段 | socket cookie | errno | 观察 |
| --- | --- | --- | --- |
| 仅 gate，新 socket | 3 | 1 / EPERM | 无快照拒绝 |
| 恢复 LSM，正常新调用 | 4 | 111 / ECONNREFUSED | token 2 快照消费为 1 |
| 旧已连接 FD 再 connect | 1 | 106 / EISCONN | token 3 快照留下 consumed 0，未到 gate |
| 拆除 LSM、改名、AF_UNSPEC 后旧 FD 重连 | 1 | 1 / EPERM | token 3 原始记录逐字节不变，仍未消费 |
| 缺 LSM 的新 socket | 5 | 1 / EPERM | 无快照拒绝 |
| 恢复 LSM，正常新调用 | 6 | 111 / ECONNREFUSED | token 7 快照消费为 1 |
| TCP_FASTOPEN_CONNECT=1 | 7 | 1 / EPERM | token 8 快照保留 consumed 0 |
| MSG_FASTOPEN | 8 | 1 / EPERM | sendmsg 被拒绝，没有回退普通 connect |
| 拆除 entry link 后原生 connect | 9 | 1 / EPERM | 没有新编号或快照 |
| 恢复 entry link，正常新调用 | 10 | 111 / ECONNREFUSED | token 9 快照消费为 1 |

这里的 ECONNREFUSED 表示 gate 放行后访问无 listener 端口，不能当成代理转发成功。普通调用使用 blocking FD、3 秒 SO_SNDTIMEO，并保存 native `connect` 的 16 字节 sockaddr、返回值与 errno。

表中另有一次 AF_UNSPEC 原生 connect：返回 0，cookie 仍为 1，TCP 状态为 CLOSE，SO_ERROR 首次读取 104、第二次为 0；它分配 token 4，但不增加 cgroup gate 的 connects 计数，也不改变已有记录。挂接后的被测序列共 10 次原生 connect（包含 AF_UNSPEC），其中 missing-entry 调用不产生编号，因此共分配 9 个编号；MSG_FASTOPEN 是另一次 sendmsg。末态按 `captures/connects/allowed/refused/fault/next_token/entries/exits` 为 `5/9/3/6/0/9/9/9`。

最终 5 条 296 字节记录与原始 map 内容逐一一致：cookie/token/consumed 分别为 `4/2/1`、`1/3/0`、`6/7/1`、`7/8/0`、`10/9/1`。旧 cookie 1 的路径仍为 `/bin/python3`；改名后的正常快照路径为 `/bin/hoopa-exe-call-token`。rename 前后以及恢复时 device/inode 均为 `2/8`。记录中的 consumed 位于 offset 20，调用编号位于 offset 32，路径位于 offset 40；旧 288 字节 ABI 不能混用。

4 个 map 为 exe_config（值 8 字节、已 freeze）、records（296 字节）、exe_state（64 字节）和 calls task storage（8 字节）。4 个程序为 capture_exe（type 29）、require_exe（type 18）以及 call_enter、call_exit（均 type 17）；原始 program info 与 fdinfo 的初始、末次 ID 一致，均有非零 JIT 长度，recursion_misses 均为 0。6 次 link 创建包含 LSM 和 entry 的重新挂接，并非同时有 6 个有效 link。原始 bytes 与 fdinfo 保留，map 属性与 FD 访问权限分别核验。

清理记录显示 6 次 link destroy 返回 0、自有 link FD 均关闭，8 个 object descriptor 关闭，自有 socket 和 cgroup FD 关闭，原文件名、同一 inode 与 comm 恢复；`btf_free_called=true` 仅表示调用了 free。证据经专用 `/dev/hvc0` 导出，普通内核日志保存在 boot.log，host checker 未发现内核事故。运行未共享宿主目录、没有网卡，未修改宿主内核或 cgroup。

本次文档独立重验了 export.json 中全部 3 份导出的长度及 SHA256，并复核归档与 4 份运行日志摘要；10 阶段 errno、64 字节原始状态、5 条 296 字节原始记录、旧记录逐字节不变及清理值均与摘要交叉比对。

| 导出文件 | 字节 | SHA256 |
| --- | ---: | --- |
| executable-probe.json | 70,734 | `411d58cfa8bc198f5a32df2ef25f351c491a330c0618c38580a14ecfc5d43df1` |
| kernel.btf | 6,018,902 | `1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9` |
| readiness.json | 1,343 | `37af1119fe7f914f600c7f322e98431bf305f6f210f8718e089633958229b508` |

归档 SHA256 为 `809accf2caf527a1ff4806e4a31d49dc6d6b438514738a7d8a7c643775afa3cb`。完整输入、运行和 checker 文件的摘要保存在机器可读记录中。

本轮有限矩阵观察到调用编号阻止旧未消费记录用于后续调用；它没有证明多线程、跨 CPU 错误总序、全部连接 API、连续 hook 覆盖、内存序或完整生命周期正确性。link 存在及 recursion_misses 为 0 也不构成连续覆盖证明。io_uring、compat/x32、内核发起调用不在支持范围。本轮没有正式名称或路径身份保证，没有核心接入，不增加 G01 正式样本（新 0、旧 0/100），不标记 T132/T137 完成。[旧未消费反例及失败运行](connector-executable-unconsumed.md)和[消费位对照](connector-executable-consumption.md)继续保留，本记录不改判历史结果。

后续固定材料复核：实际运行的[调用编号源码](../../experiments/sourceidentity/bpf/executable_call_token.bpf.c)已纳入仓库，与私有候选逐字节一致。主代理对同一原始导出完成18项离线核对（1个原件通过、17个变异拒绝），记录在私有 `t132-executable-call-token/mutation-checks.json`；新增VM为0。独立scout稳定审查未发现critical/high矛盾，未重跑VM或测试。新组合对象与v4核心接线另验，不能借用本轮运行结果。
