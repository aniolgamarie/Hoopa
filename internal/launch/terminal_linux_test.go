//go:build linux && amd64

package launch

import (
	"context"
	"encoding/json"
	"errors"
	"hoopa/internal/config"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// This wrapper is test-only and calls the newly implemented components. It has
// no history/admission/authorization behavior and is not a product run claim.
func componentTerminalWrapper(mode string) int {
	duplicate, _, errno := syscall.Syscall(syscall.SYS_FCNTL, 0, 1030, 3) // F_DUPFD_CLOEXEC
	if errno != 0 {
		return 90
	}
	stdin := os.NewFile(duplicate, "stable-component-stdin")
	defer stdin.Close()
	terminal, err := prepareTerminal(stdin)
	if err != nil || !terminal.active {
		return 90
	}
	rawfd := -1
	attrs := terminal.attributes(&rawfd)
	if attrs == nil || attrs.Ctty != int(duplicate) || attrs.Ctty <= 2 {
		return 90
	}
	// A real proof that Ctty uses the saved parent dup, not a coincidental fd0.
	if syscall.Close(0) != nil {
		return 90
	}
	self, err := os.Executable()
	if err != nil {
		return 90
	}
	cmd := exec.Command(self, "__hoopa_t056_child")
	cmd.Stdin = stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.SysProcAttr = attrs
	if cmd.Start() != nil {
		return 90
	}
	birth, birthErr := childIdentity(cmd.Process.Pid)
	componentEvent("started", map[string]any{"birth": birth, "ctty_fd": attrs.Ctty, "duplicate_fd": int(duplicate), "original_zero_closed": true, "wrapper": terminal.owner})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	observeErr := observeChildExit(ctx, rawfd)
	before, beforeErr := readLaunchProcess(cmd.Process.Pid)
	guard := false
	runtime.LockOSThread()
	var initialMask uint64
	extraMask := uint64(1) << uint(syscall.SIGUSR1-1)
	maskErr := launchThreadMask(0, &extraMask, &initialMask)
	var maskBefore uint64
	_ = launchThreadMask(0, nil, &maskBefore)
	var restoreErr error
	if birthErr != nil || observeErr != nil || beforeErr != nil || maskErr != nil {
		restoreErr = ErrLaunchTerminal
	} else {
		if mode == "tamper" {
			bad := birth
			bad.StartTicks++
			rejected := terminal.restore(bad)
			fg, err := launchForeground(stdin.Fd())
			guard = errors.Is(rejected, ErrLaunchTerminal) && err == nil && fg == int32(birth.ProcessGroup)
			if !guard {
				restoreErr = ErrLaunchTerminal
			}
		}
		if restoreErr == nil {
			restoreErr = terminal.restore(birth)
		}
	}
	var maskAfter uint64
	_ = launchThreadMask(0, nil, &maskAfter)
	_ = launchThreadMask(2, &initialMask, nil)
	runtime.UnlockOSThread()
	after, afterErr := readLaunchProcess(cmd.Process.Pid)
	fg, fgErr := launchForeground(stdin.Fd())
	componentEvent("restored", map[string]any{"before": before.identity, "state_before": before.state, "after": after.identity, "state_after": after.state, "foreground": fg, "mask_before": maskBefore, "mask_after": maskAfter, "tamper_rejected": guard, "restore_ok": restoreErr == nil && afterErr == nil && fgErr == nil})
	// The sole cmd.Wait, including failed restore. The raw pidfd remains owned
	// until this reap finishes; no other Wait or waitid runs in the components.
	waitErr := cmd.Wait()
	if rawfd >= 0 {
		syscall.Close(rawfd)
	}
	state := cmd.ProcessState
	if state == nil {
		return 90
	}
	status, ok := state.Sys().(syscall.WaitStatus)
	componentEvent("wait", map[string]any{"wait_status": uint32(status), "pid": state.Pid()})
	if !ok || restoreErr != nil || maskBefore != maskAfter || afterErr != nil || fgErr != nil {
		return 90
	}
	if waitErr != nil {
		var exit *exec.ExitError
		if !errors.As(waitErr, &exit) {
			return 90
		}
	}
	if status.Signaled() {
		return 128 + int(status.Signal())
	}
	return status.ExitStatus()
}
func TestLaunchTerminalInactiveAndUncontrolledTTYRefusal(t *testing.T) {
	if got, err := prepareTerminal(nil); got != nil || !errors.Is(err, ErrLaunchTerminal) {
		t.Fatal("nil input accepted")
	}
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	state, err := prepareTerminal(file)
	if err != nil || state.active {
		t.Fatal("nonTTY should be inactive", err)
	}
	fd := -1
	attributes := state.attributes(&fd)
	if attributes == nil || !attributes.Setpgid || attributes.Foreground || attributes.PidFD != &fd {
		t.Fatal("inactive process attributes")
	}
	attributes.Foreground = true
	if state.attributes(&fd).Foreground {
		t.Fatal("attributes share mutable state")
	}
	if state.restore(config.AppLaunchProcess{}) != nil {
		t.Fatal("inactive restoration should have no TTY operation")
	}
	file.Close()
	if _, err := prepareTerminal(file); !errors.Is(err, ErrLaunchTerminal) {
		t.Fatal("closed input accepted")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	var lock int32
	if err := launchTerminalIoctl(master.Fd(), 0x40045431, &lock); err != nil {
		t.Fatal(err)
	} // TIOCSPTLCK
	var number int32
	if err := launchTerminalIoctl(master.Fd(), 0x80045430, &number); err != nil {
		t.Fatal(err)
	} // TIOCGPTN
	slave, err := os.OpenFile("/dev/pts/"+strconv.FormatInt(int64(number), 10), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer slave.Close()
	if got, err := prepareTerminal(slave); got != nil || !errors.Is(err, ErrLaunchTerminal) {
		t.Fatal("real but non-controlling TTY silently downgraded to inactive")
	}
}
func TestLaunchTerminalNewCodeRealPTYAndPreReapRestoration(t *testing.T) {
	script := filepath.Join(t.TempDir(), "component-pty.py")
	if err := os.WriteFile(script, []byte(componentPTYDriver), 0600); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"normal", "ctrlc", "tamper"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command("/usr/bin/python3", script, self, mode)
			raw, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("owned actual component PTY failed: %v %s", err, raw)
			}
			var report map[string]json.RawMessage
			if err := json.Unmarshal(raw, &report); err != nil {
				t.Fatal("component PTY report invalid", err)
			}
			duplicate, err := componentInteger(report, "duplicate_fd")
			if err != nil || duplicate <= 2 {
				t.Fatal("did not test a stable dup >2")
			}
			t.Logf("actual PTY mode=%s %s", mode, raw)
		})
	}
}

const componentPTYDriver = `import ctypes,errno,fcntl,json,os,pty,select,subprocess,sys,termios,time
binary,mode=sys.argv[1:]
libc=ctypes.CDLL(None,use_errno=True);libc.syscall.restype=ctypes.c_long
def call(n,*args):
 r=libc.syscall(ctypes.c_long(n),*args)
 if r<0:raise OSError(ctypes.get_errno(),'exact pidfd syscall')
 return r
def check(value,message):
 if not value:raise AssertionError(message)
def own():
 os.setsid();fcntl.ioctl(0,termios.TIOCSCTTY,0);os.tcsetpgrp(0,os.getpgrp())
master,slave=pty.openpty();prior=termios.tcgetattr(slave)
p=subprocess.Popen([binary,'__hoopa_t056_terminal',mode],stdin=slave,stdout=slave,stderr=slave,preexec_fn=own,close_fds=True)
fd=call(434,ctypes.c_int(p.pid),ctypes.c_uint(0));events=[];raw=bytearray();pending=bytearray();triggered=False
try:
 deadline=time.monotonic()+30
 while time.monotonic()<deadline:
  if select.select([master],[],[],.05)[0]:
   try:data=os.read(master,65536)
   except OSError as e:
    if e.errno==errno.EIO:data=b''
    else:raise
   raw.extend(data);pending.extend(data);check(len(raw)<1<<20,'bounded output')
   while b'\n'in pending:
    line,_,pending=pending.partition(b'\n')
    if line.startswith(b'^C{'):line=line[2:]
    try:e=json.loads(line.rstrip(b'\r'))
    except ValueError:continue
    if isinstance(e,dict)and'event'in e:events.append(e)
  kinds=[e['event']for e in events]
  if not triggered and 'ready'in kinds and 'started'in kinds:
   child=next(e for e in events if e['event']=='ready');started=next(e for e in events if e['event']=='started')
   check(child['ttys']==[True,True,True]and child['foreground']==child['pid'],'child actual3FDTTY foreground')
   check(started['ctty_fd']==started['duplicate_fd']and started['ctty_fd']>2 and started['original_zero_closed'],'stable duplicate not used')
   os.write(master,b'\x03'if mode=='ctrlc'else b'exit\n');triggered=True
  poll=select.poll();poll.register(fd,select.POLLIN)
  if poll.poll(0):break
 else:raise AssertionError('bounded owned PTY deadline')
 # Drain already-written kernel bytes without using pipe EOF as a wait gate.
 for _ in range(10):
  if not select.select([master],[],[],.02)[0]:break
  try:pending.extend(os.read(master,65536))
  except OSError:break
 while b'\n'in pending:
  line,_,pending=pending.partition(b'\n')
  if line.startswith(b'^C{'):line=line[2:]
  try:e=json.loads(line.rstrip(b'\r'))
  except ValueError:continue
  if isinstance(e,dict)and'event'in e:events.append(e)
 kinds=[e['event']for e in events];check('restored'in kinds and'wait'in kinds,'new component restoration/wait evidence absent')
 restored=next(e for e in events if e['event']=='restored');started=next(e for e in events if e['event']=='started');wait=next(e for e in events if e['event']=='wait')
 check(kinds.index('restored')<kinds.index('wait'),'reaped before restore')
 for key in ['before','after']:
  held=restored[key];check(held==started['birth']and held['ppid']==p.pid and restored['state_'+key]=='Z','original child not held Z before/after restore')
 check(restored['restore_ok']and restored['foreground']==started['wrapper']['process_group'],'failed foreground restore')
 check(restored['mask_before']==restored['mask_after'],'temporary signal mask leaked')
 if mode=='tamper':check(restored['tamper_rejected'],'wrong original identity not rejected')
 code=p.wait(timeout=2);want=130 if mode=='ctrlc'else 17;check(code==want,'wrapper result')
 status=2 if mode=='ctrlc'else 17<<8;check(wait['wait_status']==status,'original child terminal wait')
 check(termios.tcgetattr(slave)==prior,'terminal settings changed')
 print(json.dumps(dict(mode=mode,duplicate_fd=started['duplicate_fd'],wrapper_exit=code,child_wait_status=status,original_before=restored['before'],original_after=restored['after'],foreground_restored=restored['foreground'],mask_before=restored['mask_before'],mask_after=restored['mask_after'],tamper_rejected=restored['tamper_rejected'])))
except BaseException:
 sys.stderr.buffer.write(raw)
 raise
finally:
 poll=select.poll();poll.register(fd,select.POLLIN)
 if not poll.poll(0):call(424,ctypes.c_int(fd),ctypes.c_int(15),ctypes.c_void_p(0),ctypes.c_uint(0))
 try:p.wait(timeout=15)
 except subprocess.TimeoutExpired:
  call(424,ctypes.c_int(fd),ctypes.c_int(9),ctypes.c_void_p(0),ctypes.c_uint(0));p.wait(timeout=2)
  raise
 finally:os.close(fd);os.close(master);os.close(slave)
`
