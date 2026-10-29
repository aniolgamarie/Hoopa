# ADR 003：SQLite 驱动共同探针，选型尚未关闭

日期：2026-10-27。状态：**进行中，T019 未完成，尚未选择生产驱动。**
基础管理仍不依赖 SQLite；这组临时 schema 和双 build tag 不进入生产账本。

## 固定输入与已测结果

共同实现为 [sqlite_probe.go](../../experiments/gost-mihomo/sqlite_probe.go)，
断言为 [sqlite_probe_test.go](../../experiments/gost-mihomo/sqlite_probe_test.go)。
两个薄包装只负责驱动注册、名称和原生错误码，不含不同的事务策略。
根 `go.mod` 没有加入 SQL 依赖；独立模块和校验记录位于
依赖准备包（本地原件 `../../private/t019-driver-acquisition/README.md`，不随Git上传）。

| 候选 | 实际 SQLite 引擎 | 当前运行证据 |
| --- | --- | --- |
| modernc.org/sqlite v1.61.0 | 3.54.1 | Linux/amd64、CGO=0 完整共同断言通过；初轮 race 的事务/中断部分通过，修订只读部分定向 race 通过 |
| github.com/mattn/go-sqlite3 v1.14.52 | 3.53.4 | Linux/amd64、CGO=1 完整共同断言及 race 通过 |

完整共同断言各有 11 个有效顶层测试、13 个子测试，0 失败、0 跳过。
另一个 `CrashChild` 顶层入口在父进程中不执行故障，明确不计入覆盖；
实际执行包含四个子进程场景：三个在屏障处阻塞并确认 kill/wait，
第四个仅尝试与提交竞争，不证明提交内发生中断。
不同 SQLite 引擎版本及编译选项已保存，不能据差异推导纯 Go 与 CGo 的因果性能结论。

共同连接参数为 `journal_mode=WAL`、`synchronous=FULL`、`foreign_keys=ON`、
`busy_timeout=50` ms、`wal_autocheckpoint=0`，在唯一持有的写连接上设置并回读。
自动 checkpoint 关闭仅服务于可控探针，不能原样当作生产空间管理方案。

已验证同一事务包含样本身份、整数增量、累计检查点、必要 gap 和数据版本；
旧样本重放先查身份，相同内容不重复计数，异内容和倒退序号/计数拒绝。
超过 2^53 的整数和 int64 上界经过真实落盘重开保持准确；新 epoch 的归零单列。
注入 fact 后/提交前失败、取消、实际 BUSY、实际页数上限 FULL，以及三个屏障中断和一个提交竞争
场景均检查重开后的完整 tuple，再以原样本身份重试。

## 首轮失败与只读边界

首轮原件（本地原件 `../../private/t019-probe-execution/attempt1/modernc-result.json`，不随Git上传）保留退出 1。
原探针错误地把“WAL/SHM 存在 + mode=ro”视为零文件变更条件，并在同一个已打开
writer 的进程中测试 chmod 后的 SHM 权限。两个顶层用例失败，没有重评为通过。

独立诊断（本地原件 `../../private/t019-readonly-diagnostic/README.md`，不随Git上传）复现了具体变化：
旧只读路径只改变 SHM 的 offset 104 一个字节，DB/WAL 未变；新进程正常读取改变
SHM 的 10 个字节。独立子进程中 WAL 或 SHM 权限为 000 时，真正查询均返回
`SQLITE_CANTOPEN`，权限恢复后内容不变。三个子进程各运行一次，均正常回收。
同进程重用已映射 SHM 是源码支持的解释，没有声称记录了系统调用轨迹。
另对 mattn 执行相同独立子进程诊断：正常查询成功并改变 SHM 的 10 字节；
WAL/SHM 000 均在取得连接时返回 CANTOPEN，文件内容不变。modernc 则在查询时
返回该错误，不能用统一的报错阶段掩盖驱动差异。mattn 首次复制诊断漏改 build tag，
只产生编译失败、未运行数据库；新副本修订 tag 后的三条件各执行一次。
SQLite 的 [WAL 文件格式](https://sqlite.org/walformat.html)说明了共享 wal-index
和 read marks；[只读 WAL 条件](https://www.sqlite.org/wal.html#readonly)也不等于
Hoopa 对 status/doctor 的零副作用承诺。

修订探针对离线直接读取 WAL 一律返回 `READ_UNAVAILABLE`，无论辅助文件是否存在。
这属于保守策略拒绝，**不算 SQLite 原生权限拒绝**。正向读取使用由显式 writer
准备并关闭的 DELETE 模式样本，查询、拒绝写入及关闭前后核对文件集合、字节、
大小、mode、mtime；已有 journal/WAL/SHM 或缺失 DB 均拒绝且不创建文件。
不通过查询切换 journaling，不使用 `immutable=1` 读取 live WAL。
该 preflight 仅用于受控稳定路径，尚不是生产路径竞态/所有权保护。
P2 服务内查询与服务不可用时的安全读取仍须各自实现。

## 证据限制与剩余工作

- `max_page_count` 确实触发 `SQLITE_FULL`，但不是物理文件系统 `ENOSPC`。
  后续[独立受限文件系统探针](../../experiments/gost-mihomo/sqlite_faultfs_linux_test.go)
  已在私有 user/mount namespace 中给两个驱动各执行一次：4 MiB tmpfs 写到真实
  ENOSPC 且可用块为零，两者 Commit 均返回原生 FULL(13)，writer 进入不确定态。
  释放填充文件、关闭重开后核验完整旧 tuple，再用原样本身份重试，完整新 tuple
  无重复。两驱动各 2 个顶层/3 个子测试及 race 通过，包含受影响的不确定返回回归。
  tmpfs 空间耗尽不证明块设备断电持久性或 fsync I/O 故障。
- 提交完成后人为隐藏返回值验证应用层不确定结果：连接禁止复用，关闭重开后按
  稳定样本身份核对。它不证明真实 fsync 失败；`race_commit` 也只证明重开得到完整
  旧态或新态，没有观测到 SIGKILL 恰在 Commit 内发生。
- modernc 的 darwin/amd64 和 windows/amd64 测试程序交叉构建通过，未在目标平台运行。
  mattn 的目标 C 工具链/SDK 尚未提供，其 macOS/Windows 构建及运行未验证。
- 两个交叉制品是测试程序，大小不能用来宣称 Hoopa 二进制或发行包预算达标。
- 新增[同步故障探针](../../experiments/gost-mihomo/sqlite_syncfault_linux_amd64_test.go)：两个驱动各 1 个顶层测试及 race 通过。一次性子进程用 seccomp TSYNC 令实际 fsync/fdatasync 返回 EIO；控制文件同步在注入前成功、注入后两 syscall 均报 EIO，故障钩子自身不返回错误。实际 Commit 均产生 primary 10 / extended 1034（[SQLITE_IOERR_FSYNC](https://sqlite.org/rescode.html#ioerr_fsync)），writer 拒绝复用。子进程正常退出回收后，未受过滤的父进程重开得到 version=1 完整旧聚合 tuple，同样本重试及再次重开得到 version=2 新 tuple。两次 close 均无错误，但没有将这点设为通用保证。此处验证内核调用层注入下的聚合事务状态，不证明逐行完整性、断电或物理设备耐久性。
- 完整权限/目录边界与平台运行证据仍需补齐，之后
  才固定唯一生产驱动及参数。T020 的资源/规模基线、T021 的 P0 整体验收保持未完成。

机器摘要与原始引用见
sqlite-probe-validation.json（本地原件 `../../experiments/gost-mihomo/sqlite-probe-validation.json`，不随Git上传）。
