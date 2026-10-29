# T019 SQLite 共同探针

这是临时账本子集，生产驱动尚未确定；结果与边界见
[ADR003](../../docs/adr/003-sqlite-driver.md)。源码和实验依赖锁入库，驱动缓存、
测试二进制、实际数据库及原始运行材料留在忽略目录中。

普通根模块构建不会装配这两个驱动。重现时用 Go 1.27.3，在新的实验目录复制：

```sh
lab=$(mktemp -d)
cp experiments/gost-mihomo/sqlite-probe.mod "$lab/go.mod"
cp experiments/gost-mihomo/sqlite-probe.sum "$lab/go.sum"
cp experiments/gost-mihomo/sqlite_probe.go "$lab/"
cp experiments/gost-mihomo/sqlite_probe_test.go "$lab/"
cp experiments/gost-mihomo/sqlite_modernc_probe.go "$lab/"
cp experiments/gost-mihomo/sqlite_mattn_probe.go "$lab/"
cd "$lab"
GOTOOLCHAIN=local go mod download
GOTOOLCHAIN=local go mod verify
CGO_ENABLED=0 GOTOOLCHAIN=local go test -mod=readonly -tags=sqlite_modernc -count=1 -timeout=90s .
CGO_ENABLED=1 GOTOOLCHAIN=local go test -mod=readonly -race -tags=sqlite_mattn -count=1 -timeout=90s .
```

需要网络时只获取锁定模块；mattn/race 需要本机 C 编译器。两种 tag 必须择一，
mattn 禁用 CGo 会编译拒绝。上述命令会创建临时数据库和测试子进程，不能在现有
用户账本上执行。首次失败记录不覆盖；修订源码或环境后另建运行目录。

`sqlite_faultfs_linux_test.go` 另需 `sqlite_faultfs` tag 和显式专用 fixture；
默认共同测试不会挂载或填满文件系统。该测试只能在私有 user/mount namespace
内的新 4 MiB tmpfs 上运行，入口环境为 `HOOPA_SQLITE_FULL_DIR`。不能将它指向
用户文件系统或已有共享 tmpfs；实际执行 wrapper 的命令、输入和结果由机器摘要
引用。文件系统耗尽与页数上限 FULL、应用层隐藏提交确认分别记录。

交叉构建可以使用 `CGO_ENABLED=0 GOOS=darwin` 或 `GOOS=windows` 配合
`go test -c -mod=readonly -tags=sqlite_modernc`。这只验证目标构建，不证明目标
文件锁、权限、WAL、休眠或恢复行为，也不代表 Hoopa 已正式支持该平台。

`sqlite_syncfault_*_test.go` 使用 `sqlite_syncfault` tag（仅 Linux/amd64），需复制三份文件并配合一个驱动 tag。可运行 `go test -race -mod=readonly -tags=sqlite_modernc,sqlite_syncfault -run '^TestSQLiteProbeNativeSyncFailure$' -count=1 -timeout=30s .`，或将驱动换成 `sqlite_mattn`。测试仅在一次性子进程安装 seccomp TSYNC/no_new_privs，把该子进程全部线程的 fsync/fdatasync 返回值改为 EIO，父进程负责等待及重开核对；不更改宿主全局策略。内核不允许过滤时测试失败，不静默跳过。它覆盖真实 SQLite 调用返回的同步错误，仍不等于断电或磁盘硬件故障。
