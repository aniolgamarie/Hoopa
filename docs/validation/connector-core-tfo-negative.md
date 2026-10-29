# 0010 核心的 TFO 同四元组拒绝回归

2026-10-24，[0010候选](../../experiments/mihomo-events/core-tfo-candidate-manifest.json) SHA256 `e9dd96f3816bcc050bfcc472f8cd578bd4bf9a26f09923790df5481ebdadd9be` 完成独立实际VM `vm-tfo-reuse-hg7flsu3`。49项离线准备检查后，主代理构建、启动固定6.18.54.1无NIC/无宿主共享guest，canonical提取与独立checker均exit0。本次记录再逐一重算24份导出的大小和SHA，并核对完整证据归档摘要。见[预登记契约](../../specs/001-hoopa-proxy-manager/contracts/connector-core-tfo-guard-v1.md)与机器记录（本地原件 `../../experiments/sourceidentity/core-tfo-negative-validation.json`，不随Git上传）。

| 阶段 | 原PID / start ticks / cookie | 请求与实际观察 |
| --- | --- | --- |
| 首个普通HTTP CONNECT | 84 / 406 / 2 | alpha.test经node:a，ready 12ms，双向33B、EOF及完整来源终末 |
| 同tuple的TFO请求 | 87 / 553 / 4 | 完整发送认证CONNECT beta.test:18995首部与33B token，共181B；随后ECONNRESET，协议回应0B |

首客户端在完整交换后abortive close、exit0/reap，再等待1,201,409,262ns，超过预登记的1.2s。第二原socket绑定相同 `127.0.0.1:31001`，向同一预登记入口 `127.0.0.1:18993` 调用一次blocking `sendmsg(MSG_FASTOPEN)`；`TCP_FASTOPEN_NO_COOKIE=1`、`SO_SNDTIMEO=3s`，返回181/181B。原client FD的TCP_INFO返回256B，state=1、options=39，host从原bytes核对SYN_DATA32，证明SYN数据已获确认；没有使用普通connect回退。reset后的getpeername返回ENOTCONN，实际远端字段保留null，未用connect参数回填。

来源仍只有首流：7帧、一个opened、一个closed33/33、无gap，epoch完整关闭，只读恢复为一条closed且tail=0。首流6517B原始前缀在第二请求及停机后逐字节不变，SHA256为 `cde9491a1cf0ba25e12c41efee30924813bdcfff469faab2b2dce6de144da411`。独立node:a和target各仅记录首token的33/33B，没有node:b、第二token或额外flow。累计admitted1/rejected1/revokedtrue，最终live gauge0；两个客户端均exit0/reap，核心按脚本SIGTERM后exit0。guest的tcp_fastopen从1设为1543（0x607）并恢复1；原11FD移交、kernel/BTF、导出完整性、线程清理及独立virtio通道核对通过，boot日志无所检查的内核异常。

这是一轮有限的实际拒绝行为记录，没有独立记录具体内部拒绝分支，也没有复现旧核心错误归属竞态。[原accepted TCP_INFO标志观察](connector-tfo-boundary.md)、固定源码与单测提供检查顺序的另行依据；本轮不扩展为data-less TFO、所有握手时序或跨内核保证。[两协议early-SHUT_WR正向回归](connector-core-tfo-positive.md)单独保留。旧制品和旧结果不移作新hash的通过证据；正式identity仍unverified、G01新增0，T132/T137不因此完成。
