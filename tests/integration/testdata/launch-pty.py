"""Independent owned-session observer for the actual pinned hoopa run command."""
import base64,ctypes,errno,fcntl,json,os,pathlib,pty,select,signal,subprocess,sys,termios,time
cli,fixture,mode=sys.argv[1:]
libc=ctypes.CDLL(None,use_errno=True);libc.syscall.restype=ctypes.c_long
def call(number,*args):
 result=libc.syscall(ctypes.c_long(number),*args)
 if result<0:raise OSError(ctypes.get_errno(),'original pidfd syscall failed')
 return result
def stat(pid):
 raw=pathlib.Path('/proc/%d/stat'%pid).read_text();a=raw.index('(');b=raw.rindex(')');f=raw[b+1:].split()
 return dict(pid=int(raw[:a]),parent=int(f[1]),state=f[0],start=int(f[19]))
def original(pid):
 before=stat(pid);fd=call(434,ctypes.c_int(pid),ctypes.c_uint(0));after=stat(pid)
 if before['start']!=after['start']:os.close(fd);raise ValueError('original identity changed')
 return fd,before
def ready(fd):
 poll=select.poll();poll.register(fd,select.POLLIN);events=poll.poll(0)
 if events and events[0][1]&select.POLLNVAL:raise ValueError('invalid original pidfd')
 return bool(events and events[0][1]&select.POLLIN)
def foreground(fd,group):
 old=signal.pthread_sigmask(signal.SIG_BLOCK,{signal.SIGTTOU})
 try:os.tcsetpgrp(fd,group)
 finally:signal.pthread_sigmask(signal.SIG_SETMASK,old)
def check(ok,message):
 if not ok:raise AssertionError(message)
master,slave=pty.openpty();os.setsid();fcntl.ioctl(slave,termios.TIOCSCTTY,0);foreground(slave,os.getpgrp());before=termios.tcgetattr(slave)
def child_group():
 os.setpgid(0,0);foreground(0,os.getpgrp())
p=subprocess.Popen([cli,'run','--app','app-a','--mode','direct','--',fixture,mode],stdin=slave,stdout=slave,stderr=slave,preexec_fn=child_group,close_fds=True)
wrapperfd,wrapper_identity=original(p.pid);rootfd=None;raw=bytearray();pending=bytearray();fixture_ready=None;root_identity=None;triggered=False
try:
 deadline=time.monotonic()+40
 while time.monotonic()<deadline:
  if select.select([master],[],[],.05)[0]:
   try:data=os.read(master,65536)
   except OSError as e:
    if e.errno==errno.EIO:data=b''
    else:raise
   raw.extend(data);pending.extend(data);check(len(raw)<=1<<20,'bounded PTY output exceeded')
   while b'\n'in pending:
    line,_,pending=pending.partition(b'\n')
    if line.startswith(b'^C{'):line=line[2:]
    try:event=json.loads(line.rstrip(b'\r'))
    except (ValueError,UnicodeError):continue
    if event.get('event')=='fixture_ready':
     fixture_ready=event;rootfd,root_identity=original(event['pid'])
  if fixture_ready is not None and not triggered:
   check(fixture_ready['tty']==[True,True,True] and fixture_ready['foreground']==fixture_ready['pid'] and fixture_ready['pgrp']==fixture_ready['pid'],'actual child not foreground on all3TTY descriptors')
   os.write(master,b'PTY_INPUT\n' if mode=='tty-normal' else b'\x03');triggered=True
  if ready(wrapperfd):break
 else:raise AssertionError('CLI/PTY terminal deadline is not successful rejection')
 # Observe wrapper terminal WITHOUT reaping it, so its group/PID cannot be
 # recycled before the independent TTY restoration check. No product event used.
 observed=stat(p.pid);check(observed['state']=='Z' and observed['parent']==os.getpid() and observed['start']==wrapper_identity['start'],'wrapper not held original unreaped child')
 after=os.tcgetpgrp(slave);check(after==p.pid,'product did not restore prior wrapper foreground group')
 check(fixture_ready is not None and rootfd is not None and ready(rootfd),'original application not terminal')
 unchanged=termios.tcgetattr(slave)==before;check(unchanged,'product altered terminal settings')
 code=p.wait(timeout=2);want=17 if mode=='tty-normal' else 130;check(code==want,'actual run exit mapping')
 for _ in range(5):
  if select.select([master],[],[],.02)[0]:
   try:raw.extend(os.read(master,65536))
   except OSError:break
 report=dict(wrapper_pid=p.pid,root_pid=fixture_ready['pid'],root_start_ticks=root_identity['start'],child_foreground=fixture_ready['foreground'],after_foreground=after,wrapper_exit=code,tty=fixture_ready['tty'],raw=base64.b64encode(raw).decode(),termios_unchanged=unchanged,wrapper_unreaped=True)
 print(json.dumps(report))
finally:
 if not ready(wrapperfd):
  call(424,ctypes.c_int(wrapperfd),ctypes.c_int(signal.SIGTERM),ctypes.c_void_p(0),ctypes.c_uint(0))
 try:p.wait(timeout=35)
 except subprocess.TimeoutExpired:
  call(424,ctypes.c_int(wrapperfd),ctypes.c_int(signal.SIGKILL),ctypes.c_void_p(0),ctypes.c_uint(0))
  p.wait(timeout=2)
  raise
 finally:
  # Only this independent observer's completed cleanup: application execution
  # and all foreground/termios/result assertions happened before this point.
  # Do not move before Popen, where it would alter child signal inheritance.
  signal.signal(signal.SIGHUP,signal.SIG_IGN)
  foreground(slave,os.getpgrp())
  os.close(wrapperfd)
  if rootfd is not None:os.close(rootfd)
  os.close(master);os.close(slave)
