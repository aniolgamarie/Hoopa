# Executable 组合核心：两协议正向验证

2026-10-24，固定 001–012 候选通过[组合原型契约](../../specs/001-hoopa-proxy-manager/contracts/connector-executable-online-v1.md)的本轮有限正向判据。两次实际 VM 必须分记：首轮 001–011 因线路证据不合格而失败；新增 0012 后的新一轮成功。不能记成两轮通过。机器可读字段、导出摘要与私有证据索引见 executable-core-positive-validation.json（本地原件 `../../experiments/sourceidentity/executable-core-positive-validation.json`，不随Git上传），制品链见[新候选 manifest](../../experiments/mihomo-events/core-executable-quality-candidate-manifest.json)。

首轮 `vm-core-executable-positive-oqznnj4m` 虽然完成两条 33/33B 转发并输出 v4 executable，但 actual route 的 adapter type 为 `unverified`、node ID 为 null，host acceptance 失败。固定 001–011 核心 SHA256 为 `4fab2b7f44513d57c2629a7b68c3c4ee4634130a10135e59930b0f57925788bb`。其原始运行、失败报告、manifest 和证据检查点保留；本次只读重验检查点的全部 21 个文件，字节与摘要未变。

源码定位到共享 `HoopaQualityEnabled` 初始化仅识别 profile 2/3。独立 [0012 补丁](../../experiments/mihomo-events/patches/0012-enable-executable-quality-capture.patch)只追加 literal profile 4，并以当前 test 二进制的真实子进程初始化覆盖空值、1、2、3、4、03、04、invalid。constant 的 1 个顶级测试和 8 个子用例通过；profile 4 环境下，3 个 outbound 定向测试（含子测试共 13 项）通过。旧失败没有因此改判。

新成功运行是 `private/t132-executable-core-positive-v2/vm-core-executable-positive-2g3tuta8`。主代理完成 76 项离线准备检查、1 次真实 VM、canonical extractor（退出 0）和 host checker（退出 0）。QEMU 退出 0，用时 9.138 秒；核心 PID 77 经 SIGTERM 正常退出 0；guest 与 runner 的错误列表为空。本文编写阶段仅只读比对既有材料，没有重跑 VM、BPF 或测试。独立 scout 只读审查已完成，未发现本轮有限判据的 critical/high 证据矛盾，并独立核对 21 份导出和原失败检查点；审查结论不扩大本轮范围。

| 客户端 | 原 PID/TID / writer PID | 实际线路 | ready 耗时 | exe call token | 双向终末 |
| --- | --- | --- | ---: | --- | --- |
| HTTP CONNECT → alpha.test | 84 / 84 / 84 | node:a，Http | 12 ms | `1` | 33 / 33 B |
| SOCKS5 → beta.test | 87 / 87 / 87 | node:b，Http | 31 ms | `3` | 33 / 33 B |

两客户端各使用独立原 TCP socket。HTTP 的 client/accepted cookie 为 `4097/4098`，SOCKS5 为 `2/3`；本地端口分别为 31001、31002，原入口均为 `127.0.0.1:18993`。每条流先发送 33B，再执行 SHUT_WR，随后收齐相同的 33B 和 EOF；顺序为 `send33 → shutdown-wr → response-eof`。两个自有客户端均退出 0、被 wait/reap，随后 `/proc/<pid>` 不存在。node:a、node:b 与独立 target 的实际接收/发送记录逐流绑定各自预登记 payload，均为 33B；公开摘要只保存 payload SHA256，不公开认证内容。

opened 为 [source v4](../../specs/001-hoopa-proxy-manager/contracts/source-connector-v4.md)，connector schema 为 `sourceidentity-core-admission-v2`。两份完整 296B executable 原始记录均为 consumed 1，路径为 `/bin/python3`，basename 为 `python3`。cookie、TGID、caller TID 与原 connect 记录、独立 socket oracle 相同，call token 非零且两流不同。原始记录摘要分别为：

- HTTP：`6c084ad1d7bd83f9fe187d6c569472e2496b725fa9ae01cc9411453d0631c00a`。
- SOCKS5：`d9f0cffa7acebdcf00e4f567d309774edb3d48c19f7d7b74b9d4803f2fd07ebf`。

每个客户端在 connect 前后独立读取 `/proc/self/exe` 原字节及 stat；两次均为同一 device 2、inode 8，路径和 basename 与内核快照一致。该 oracle 仅用于事后核对，不进入核心准入。basename 来源与 comm 分开保存；本样本中二者都是 `python3`，不能据此混同其含义。

| 流 | leader start BOOT ns | executable BOOT ns | connect BOOT ns |
| --- | ---: | ---: | ---: |
| HTTP | `4034546632` | `4212707247` | `4212711626` |
| SOCKS5 | `4253091624` | `4576992681` | `4576998205` |

两条均满足 `leader_start ≤ exe_boot ≤ connect_boot`，exe 与 connect 的时间还落在该客户端实际 connect 的 BOOT 包围区间内。leader start 对应独立 start ticks 403、425。BOOT 身份时间与 setup quality 的单调时钟坐标分别核对，没有跨口径相减。112B connect 记录没有 token 字段；同调用编号检查来自组合 gate 的执行逻辑和[调用编号原型](connector-executable-call-token.md)，不能声称两个导出记录直接比较了 token。

same-PID exec 的 intent PID 与实际核心 PID 77 相同；`hoopa-core-identity-handoff-v2` 移交 9 maps、6 programs、5 links，共 20 个不同 FD。核心 ready 时逐个检查这 20 个已登记 FD 的 fdinfo，均已恢复 CLOEXEC；没有扫描瞬时普通 FD。实际 guest LSM 列表包含 bpf，目标内核为固定 6.18.54.1；专用 `/dev/hvc0` 传证据，boot.log 单独保留内核日志，未发现 BUG/lockup/Oops/panic。运行无网卡、无宿主目录共享，也不修改宿主 BPF、cgroup 或 sysctl。

ready、第一流结束、第二流结束三个采样点的当前 live TCP gauge 都为 0；累计 admitted 分别为 0、1、2，rejected 始终 0、revoked 始终 false。当前 live gauge 不是累计连接数。identity_instance、identity_name、identity_path 全程为 false，legacy process 仍为 unknown，legacy process_path 仍为 unknown。

来源包含 13 个原始帧，两条流各有同 flow 的 opened、实际线路、connection binding、成功 ready 与 closed，最后为完整 epoch_closed，没有 gap。两条 closed 的整数终末均为 33/33，连接段守恒。新的只读 wire v4 decoder/state 和 source recovery 工具已由主代理执行；本记录独立复核 len/raw/SHA 分帧，并与保存的严格解码结果逐帧相等。恢复结果为两条 closed、各 tail_bytes 0、epoch finality complete。身份保留在 opened 原帧及解码结果中，RecoveryFlow 没有新增 executable 身份展示。

成功运行的全部 21 份导出均独立重验长度和 SHA256，归档摘要与专用通道 envelope 相符。核心、对象与镜像的固定 pin 如下；完整导出清单见机器可读记录。

| 制品 | 字节 | SHA256 |
| --- | ---: | --- |
| 001–012 核心 | 51,243,330 | `4bc907d7c675ec886b1342f6b05f675f693b2d72f21c5da8557d4958f1d68d27` |
| 组合 BPF 对象 | 905,720 | `1bf999df3affd167b06c923b7dde3ef27ec521e7e0afaf2accb3f447a6059500` |
| 本轮镜像 | 41,974,930 | `71b38e3e544cf7973a16e3d682e2e7f51229f576132a12b865abf753d42e4717` |
| 原 source frames | 15,875 | `bc61222b9446a7c64a792ae71d2263fd44aa2d0ef79c4d54dfd228dda85d6290` |

证据归档 SHA256 为 `8cf8acf6465ae22692f61bdd902b0851f58c81abd53e0ca15544a74b8d9bd4d7`。新 manifest 保持其“创建时 VM 未验证”的历史状态，本验收记录补充后续实际观察，不回写旧记录。

本轮仅覆盖可信独占 loader、固定隔离 guest、两个单线程普通 native connect 客户端的正向场景；不新增 TFO 拒绝、并发、namespace、长期回收或全部连接 API 的组合验收。原 writer gate 只覆盖 connect/sockops；其 drain 不能证明 raw/LSM 全部写入停止，正常退出也不构成 RCU 结束证明。名称、路径与实例能力仍 unverified/false，不开放 PROCESS 规则，不把局部通过升为正式身份支持。T132/T137 不完成，总计 139 项/21 完成、正式新增 0、旧 0/100 均保持。
