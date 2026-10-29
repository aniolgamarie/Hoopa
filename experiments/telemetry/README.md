# Linux 实验事件 journal

本包只保证已送达本进程、并在 Append 完整写入和 Sync 后确认的事件按 epoch + event ID 去重与重放。不修复 GOST 未投递，不证明源事件完整、真实原进程身份、最终字节或路由前决策；不是生产 SQL、事件来源 adapter 或 G01–G07 验收闭合。它与现有组合 harness 尚未接线。

`OpenJournal(privateExistingDir)` 要求已存在的 0700 目录，使用原目录的 os.Root 句柄。固定文件为 journal.lock 和 events.frames，均须常规文件且权限恰为 0600；拒绝 symlink、不安全模式和目录替代。Linux 非阻塞 flock 在 Journal 全生命周期持有，第二 writer 返回 ErrLocked；这是配合使用本包的进程之间的 advisory lock，外部程序直接改文件不受此锁保护。调用者应保持私有目录及文件名不被外部替换。

`Append(epoch, eventID, payload)` 的 epoch 和 ID 使用 internal/config.ParseID 的 1–128 字节 ASCII token 规则。payload 必须是最多 64 KiB 的单一有效 JSON 对象。相等按原始字节定义，包含所有空白：同 epoch/ID 与相同字节返回原 seq，不再写盘；不同字节返回 ErrConflict，不进行 JSON 语义归一。调用者须在 Append 返回前保持输入缓冲区不变。Replay 返回 seq 顺序的深拷贝，调用者修改不会改变 journal。

磁盘格式版本 1：4 字节 big-endian uint32 envelope 长度、JSON envelope、32 字节 SHA256。envelope 严格包含 format_version、seq、epoch、id、payload；payload 是原始字节的 base64 字符串。base64 序列化有约三分之一开销，因此 envelope 最大 96 KiB；扫描同时检查 envelope 上限与解码后 64 KiB 上限。原始 JSON 只以 RawMessage/UseNumber 校验，绝不经 float64，2^53+1、uint64 整数字面和十进制字节字符串原样保留。checksum 检测损坏，不提供恶意修改防护或来源认证。

seq 从 1 严格递增。打开时完整扫描，核对 hash、版本、ID、序号、重复外层字段与重复事件键；无论新建或重开，均在数据 Sync、父目录 Sync 成功后才返回可用实例。完整帧即使来自此前结果不确定的 Append，也只有经扫描和再次同步后才能消歧为原结果。

Append 仅在完整 frame Write 和 Sync 都成功后推进内存索引与确认位置。短写、Write 错误或 Sync 错误均返回 ErrUncertain，实例随后拒绝所有 Append，禁止盲重试。Replay 在该实例仍只展示已确认记录；关闭并重开后，完整有效帧可按原 ID 消歧。半帧、坏 hash、跳号、重复键、非法或超限数据返回 ErrRecoveryRequired，保留文件原样，不自动截尾、跳过损坏或当作空库。修复策略须交主代理裁决。

其他错误分类可用 errors.Is 判断：ErrInvalid（输入）、ErrUnsafe（文件或权限）、ErrClosed、ErrIO。Open 失败不提供 Journal，保留已有文件并释放所持锁；Close 释放资源和锁。单实例方法以 mutex 串行化；Close 后 Append 拒绝，Replay 仍可查看该实例最后的已确认深拷贝。

在仓库根目录运行以下检查；仅使用标准库，不绑定 socket、不启动核心、不访问网络、不安装模块：

```sh
/tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/gofmt -l experiments/telemetry
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go vet ./experiments/telemetry
GOTOOLCHAIN=local GOCACHE=/tmp/hoopa-go-cache GOMODCACHE=/tmp/hoopa-go-modcache GOPROXY=off \
  /tmp/hoopa-go-1.27.3-cb6ry1lq/go/bin/go test -race -v ./experiments/telemetry
```

Write/Sync 故障通过未导出的文件接口注入，验证结果不确定后的 poison 与重开消歧。真实断电、文件系统持久化保证及未送达事件的恢复未由这些测试证明。
