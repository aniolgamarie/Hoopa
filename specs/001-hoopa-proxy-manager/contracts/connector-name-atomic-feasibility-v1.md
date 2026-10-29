# 名称观测所需指令与挂接的有限可行性探针

2026-10-24，T132 研究子步骤。现有名称字段保持 unverified，固定 001–009 核心和已有 BPF 不改。本探针不是名称身份实现，也不将“可加载”当作完整证明。

仅在既有无网卡、无宿主共享的 Linux 6.18.54.1/x86_64 VM 执行一个独立 BPF 对象：两个程序分别 fentry/fexit 挂接 `__set_task_comm`，第三个程序挂在 guest 专用 cgroup 的 connect4。共用一个只读于用户态的状态 ARRAY，以有限 CAS 操作组合 sequence/active；配置 ARRAY 仅指定自有测试 TGID，加载后初始化并 freeze。指令原型在私有目录，禁止默认宿主执行。

必须从实际 guest BTF 找到目标 FUNC 和 FUNC_PROTO，记录函数 ID、参数类型 ID 及整份 BTF 摘要。挂接后初值全零；自有单线程进程执行两次确定的 PR_SET_NAME，读回自有 comm；再连接无 listener 的指定 loopback 端口，必须观察 ECONNREFUSED 且 connect4 程序执行一次。状态应为 sequence=4、active=0、poison=0、entry=2、exit=2、connect=1。三个程序记录真实类型/ID、JIT 长度及 recursion_misses=0；逆序销毁自有 link、释放对象/FD，解除挂接后恢复自有测试进程名。

通过只能说明这份内核上目标 BTF/挂接及所用原子指令的小样本执行成立。它不读取或认证连接进程名，不处理并发改名，不接入核心准入，不证明内存序、漏钩子检测、启动/撤销边界、完整内核写入路径或生产权限模型。相关条件必须分别验证，不能修改 T132/T137 或正式 G01 能力。

原始结果通过独立 virtio console 证据通道导出，普通串口保留启动与内核日志；严格检查拒绝证据损坏、预期不符或内核 BUG/Oops/soft lockup。失败原始材料保留，每次新运行单独登记输入和判据。
