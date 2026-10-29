# P0 真实工作流取证

日期：2026-10-22。结论：受控转发、独立认证、真实 Codex 正常与持续响应已通过本次子断言；P0 尚未通过。脱敏机器摘要与原始文件 SHA-256 见 p0-2026-10-22.summary.json（本地原件 `p0-2026-10-22.summary.json`，不随Git上传）。原始配置、目标、请求内容、Agent 输出和完整标签仅保留在私有实验目录，不提交 Git。

## 固定输入

Linux amd64，Go 1.27.3；未修改系统 Go、旧代理或父 shell 代理变量。使用已有二进制，不执行核心安装或升级：

| 角色 | 实测版本 | SHA-256 |
| --- | --- | --- |
| Mihomo | 1.19.26 | `78f3d225a53f8345db5b1f3f147d155ef5a1d56843931f6d61f908045cefb789` |
| GOST | 3.3.1 | `04ee3969e59fe86924616002464145be47caf4a89f0501efdfb6458f206ddc4f` |
| Codex | 0.163.0 | 见摘要的 binaries，按本地文件计算 |

版本与文件摘要是已观察事实；未校验它们对应官方发布资产或固定源码，因此 manifest 中 `source_verified=false`、`source_revision=null`。不能据此宣称完成上游供应链审计。

## 受控校准

复现构建、参数和认证配置见[实验说明](../../experiments/gost-mihomo/README.md)。首轮私有目录为 `/tmp/hoopa-p0-1749983525`，结束时已清理本次启动的核心。

Mihomo 共享入口、GOST HTTP→Mihomo、GOST SOCKS5→Mihomo 三条路径各运行 7 项：32 KiB 下载、16 KiB 上传、HTTPS CONNECT、128 KiB 长连接、503、100 ms 超时及固定 TCP 收发。21/21 子断言通过。客户端往返仅标为 `client_round_trip_ms`。

controller 与 metrics 各自授权成功/未授权失败、两种 HTTP 数据入口各自授权成功/未授权失败，加 SOCKS5 认证拒绝与成功，共 9/9 子断言通过。这不是完整 G07 安全/规模验收；本地管理 IPC 尚未实现。

首轮按相同完整标签集合逐 series 差分：input `72198`、output `466001` 字节，以十进制 JSON 字符串保存。这个结果只证明实验差分可用，不代表应用净 payload、入口计数和终末明细已同口径对账。

## 真实 Codex

私有目录为 `/tmp/hoopa-p0-3818774022`。以 harness 的 `--keep-cores --upstream-proxy 127.0.0.1:10808` 保留本次两核，显式使用已有本地 SOCKS5 作为上游；旧实例未被修改。测试期间父 Python 会话保持运行，harness 本身已退出。

只在目标 Codex 子进程中设置 HTTP_PROXY/HTTPS_PROXY 及小写变量指向私有 GOST HTTP URL，ALL_PROXY 及小写变量指向 GOST SOCKS5 URL，NO_PROXY/no_proxy 为空。使用继承的现有 Agent 认证；未读取或打印其凭据。父进程的代理环境在前后逐键对比一致。

目标命令形式如下，参数为数组，不经过 shell 拼接；工作目录为本次新建私有空目录：

```text
codex exec --ephemeral --skip-git-repo-check --sandbox read-only --color never --json -C <private-directory> -
```

stdin 分别传入一个无工具短回答请求、一个无工具多行持续回答请求；内容不作为公共证据保存。stdout/stderr、计数前后快照及每 100 ms 的活动连接快照以 0600 写入私有目录。

| 工作流 | 应用完成信号 | 实际耗时 | GOST input 增量 | GOST output 增量 |
| --- | --- | --- | --- | --- |
| normal | exit 0、turn.completed、预期标记一致 | 4205 ms | `113859` | `6042820` |
| sustained | exit 0、turn.completed、持续响应完成 | 29446 ms | `220275` | `33388712` |

两次均有字节增长及活动连接；Mihomo 当时实际链为 `UPSTREAM`，记录到的进程名为 `gost`。这证明应用通过实验代理路径完成联网，也直接暴露了跨 GOST 后的源身份边界。该数值包含 Agent 的实际网络活动，不能解释为回答文本大小、Token 或供应商费用；未解密业务 TLS，也没有断言业务首字节时间或外网 IP 与两个供应商出口一致。

测试结束依次核验 boot ID、PID/starttime、实际 executable 摘要、cwd、配置参数和 run nonce，再通过 Linux pidfd 发送信号。两核均记录 `ORIGINAL_PROCESS_EXIT_VERIFIED`。这只证明原实例已退出，不证明生产接管协议。

## 限制与执行修正

第一次跨工具会话保留实验后，核心已不存在，后续连接被拒绝；执行环境可能清理其进程族，尚未隔离原因。改在同一父会话中运行真实 Agent 后成功。第一次正常请求虽成功，取证脚本随后因 Python Decimal 不接受 bytes 及空连接数组处理出错；修正后完整重跑上述两工作流，旧失败记录未作为成功证据。

尚未验收 SIGINT/终端信号透传、SSH/注销/休眠、生产 wrapper、两供应商出口一致性、采集故障完整恢复或所有短连接。正常应用响应与退出码不能替代这些场景。参见 [G01](g01-identity.md)、[G02](g02-counters.md)、[G03](g03-quality.md) 和[核心门槛裁决](../adr/002-p0-core-gates.md)。
