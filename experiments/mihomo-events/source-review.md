# Mihomo v1.19.26 固定源码审查

日期：2026-10-22。范围：只读源码/版本审查；没有增强核心构建或新代理流量。
源码固定为官方 `v1.19.26` / `fc8c5a24b16991f98cd736950c17d1aa306a5041`；
文件摘要见 [source.lock.json](source.lock.json)，后续改动提案见 [upstream-proposal.md](upstream-proposal.md)。
本文所有行号均指该固定上游源码，不是 Hoopa 内的同名文件。

| 问题 | 固定源码位置 | 本次确认的事实及限制 |
|---|---|---|
| 原始入口 | `listener/socks/tcp.go:175`、`listener/http/proxy.go:49`、`adapter/inbound/socket.go:11` | 握手/目标解析后移交连接，可取得真实 peer/local tuple；转发器后的 tuple 只能指向转发器。声明的地址或 PROXY header 不能冒充内核 socket 证据 |
| 进程查询 | `component/process/process.go:21`、`process_linux.go:65`、`tunnel/tunnel.go:354` | 公共返回值只有 UID/path；Linux 依据 inode 搜索并返回首个匹配者，不证明 owner 唯一，也不返回 PID/start |
| 进程规则 | `rules/common/process.go:32`、`tunnel/tunnel.go:640` | 查询失败可留下空字段并继续后续规则；标准名称/路径匹配还采用大小写不敏感语义，不能直接代表 Hoopa 的精确条件和 unknown 拒绝 |
| 入站指定代理 | `listener/inbound/base.go:88`、`tunnel/tunnel.go:315` | listener `proxy` 被写入 SpecialProxy，优先返回而绕过规则序列；应用规则路径必须不设置它 |
| 拨号与重试 | `tunnel/tunnel.go:562`、`:702` | retry 内每次调用都要独立 attempt；只有成功后才创建 tracker 会遗漏失败连接与尝试 |
| 实际 ready | `tunnel/tunnel.go:568`、`:601` | 延迟握手和 peek 写入发生在 tracker 之前；这一位置仅是候选观测点，仍须协议实际验证，不能以 Dial 返回推断所有通道 ready |
| 字节计数 | `tunnel/statistic/tracker.go:52`、`:72`、`:95`；`common/net/sing.go:67` | 普通 Read/Write 用返回 n，buffer 与 CountFunc 路径口径不同；预读上传还通过 peekLen 初始化。部分失败、重试、buffer 快路径必须另外验证 |
| 线路与终末 | `tunnel/statistic/tracker.go:119`、`:110`；`manager.go:40`、`:84` | chain/provider 在实际 Conn 上取得；关闭后 Leave 从活动集合删除，活动快照不能重建终末历史 |

因此，单独删除 GOST、提高快照轮询频率或增加 PID 字段均不构成完整修复。
目前没有源码证据表明需要重写代理协议或默认特权；也没有证据证明普通权限在当前宿主即可得到完整 owner 集合。
既有 [Linux 原始身份探针](../sourceidentity/README.md)的权限未验证结论保持，不以首次命中 PID 代替唯一归属。

许可核对读取的是固定标签 `LICENSE` 的 GNU GPL version 3 文本。Hoopa 的独立管理器不导入 Mihomo 库；
若准备修改核心，新增/修改核心源码与其许可证义务单独记录。此次没有分发修改核心或把其实现复制到 Hoopa 管理器。
上游依赖由固定 `go.mod` 记录；本次没有新增依赖、没有核验所有传递依赖的发行义务，因此不能宣称修改核心已可发行。

官方资料：

- [固定 commit](https://github.com/MetaCubeX/mihomo/tree/fc8c5a24b16991f98cd736950c17d1aa306a5041)
- [process API](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/component/process/process.go)
- [Linux 查询](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/component/process/process_linux.go)
- [TCP/规则路径](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/tunnel/tunnel.go)
- [tracker](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/tunnel/statistic/tracker.go)
- [LICENSE](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/LICENSE)
- [固定 go.mod](https://github.com/MetaCubeX/mihomo/blob/fc8c5a24b16991f98cd736950c17d1aa306a5041/go.mod)
