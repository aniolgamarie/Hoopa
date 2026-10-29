"""Executable-online exact resource/ownership tests; fake libbpf only."""
import contextlib
import ctypes
import errno
import hashlib
import io
import json
import os
from pathlib import Path
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import Mock, patch

import exec_core
import inspect_object
import native_loader
from native_loader import NativeBackend, NativeError, EXECUTABLE_MAP_REFS, EXECUTABLE_PROGRAM_TYPES
from observations import expected_maps
from online_admission import AdmissionOwner
import test_exec_core as bootstrap_tests
from test_exec_core import Copies, handoff, arguments
from test_native_loader import Function
from test_online_profile import OnlineLibrary, CONFIG_RAW


class ExecutableLibrary(OnlineLibrary):
    def __init__(self):
        super().__init__()
        self.metadata = expected_maps("executable-online")
        self.maps = {name: 20+i for i, name in enumerate(self.metadata)}
        self.programs = {name: 40+i for i, name in enumerate(EXECUTABLE_PROGRAM_TYPES)}
        self.types = dict(EXECUTABLE_PROGRAM_TYPES)
        self.map_references = {name: [self.maps[key]+100 for key in sorted(names)]
                               for name, names in EXECUTABLE_MAP_REFS.items()}
        self.values = {name: {} for name in self.maps}
        for name, size in (("capture_config",32),("capture_faults",8),("capture_gate",40),("exe_state",64)):
            self.values[name][bytes(4)] = bytes(size)
        self.pointer_program = {}
        self.destroyed = []
        self.btf_data = b"test BTF"
        self.btf_size = len(self.btf_data)
        self.btf_id = 73
        self.func_id = 39733
        self.btf_ret = 900
        self.name_override = None
        self.destroy_ret = 0
        for name in ("bpf_program__attach", "bpf_link__fd", "bpf_link__destroy", "bpf_btf_get_fd_by_id"):
            setattr(self, name, Function(lambda *a, name=name: self.invoke(name,*a)))

    def invoke(self,name,*args):
        if name in ("bpf_program__attach","bpf_link__fd","bpf_link__destroy","bpf_btf_get_fd_by_id"):
            self.calls.append((name,args))
            if name=="bpf_program__attach":
                ptr=700+args[0];self.pointer_program[ptr]=args[0];return ptr
            if name=="bpf_link__fd":return args[0]+1000
            if name=="bpf_link__destroy":self.destroyed.append(args[0]);return self.destroy_ret
            return self.btf_ret
        if name=="bpf_program__section_name":
            self.calls.append((name,args));program=next(n for n,p in self.programs.items() if p==args[0])
            return inspect_object.PROGRAMS["executable-online"][program].encode()
        return super().invoke(name,*args)

    def info(self,fd,output,length):
        if fd==900:
            pointer,capacity=struct.unpack_from("<QI",output.raw)
            ctypes.memmove(pointer,self.btf_data,min(capacity,len(self.btf_data)))
            raw=struct.pack("<QIIQII",pointer,self.btf_size,self.btf_id,0,0,1)
            ctypes.memmove(output,raw,32);ctypes.cast(length,ctypes.POINTER(ctypes.c_uint32))[0]=32
            return 0
        if fd-1000 in self.pointer_program:
            prog=self.pointer_program[fd-1000];name=next(n for n,p in self.programs.items() if p==prog)
            raw=bytearray(256);struct.pack_into("<III",raw,0,2 if name=="capture_exe" else 1,fd+500,prog+100)
            if name=="capture_exe":struct.pack_into("<III",raw,16,27,self.btf_id,self.func_id)
            else:
                pointer,capacity=struct.unpack_from("<QI",output.raw,16)
                target=self.name_override or (b"sys_enter" if name=="call_enter" else b"sys_exit")
                ctypes.memmove(pointer,target+b"\0",min(capacity,len(target)+1))
                struct.pack_into("<I",raw,24,len(target)+1)
            if self.bad_info:self.bad_info(fd,raw)
            ctypes.memmove(output,bytes(raw),256);ctypes.cast(length,ctypes.POINTER(ctypes.c_uint32))[0]=256
            return 0
        ret=super().info(fd,output,length)
        if fd==self.programs["capture_exe"]:
            struct.pack_into("<II",output,220,self.btf_id,self.func_id)
        return ret


class ExecutableOnlineTests(unittest.TestCase):
    def setUp(self):
        self.lib=ExecutableLibrary()
        for p in (patch("inspect_object.ctypes.CDLL",return_value=self.lib),
                  patch.object(NativeBackend,"_fd_access",side_effect=self.lib.fd_access),
                  patch.object(native_loader,"TARGET_BTF_BYTES",len(self.lib.btf_data)),
                  patch.object(native_loader,"TARGET_BTF_SHA256",hashlib.sha256(self.lib.btf_data).hexdigest())):
            p.start();self.addCleanup(p.stop)
        self.close=patch("native_loader.os.close").start();self.addCleanup(patch.stopall)
        self.backend=NativeBackend("fake",b"object",profile="executable-online")

    def ready(self):
        self.backend.open_object();self.backend.load_object();return self.backend.verify_loaded()

    def test_exact_inventory_references_and_target_btf_once(self):
        self.assertEqual(self.lib.calls,[])
        result=self.ready()
        self.assertEqual(len(result["maps"]),9);self.assertEqual(len(result["programs"]),6)
        self.assertEqual(set(self.backend.programs),set(EXECUTABLE_MAP_REFS))
        self.assertTrue(self.backend.target_btf_verified)
        self.close.assert_called_once_with(900)
        self.backend.verify_loaded()
        self.assertEqual(sum(n=="bpf_btf_get_fd_by_id" for n,_ in self.lib.calls),1)
        self.assertEqual(self.lib.bpf_btf_get_fd_by_id.argtypes,[ctypes.c_uint32])
        self.assertEqual(self.lib.bpf_program__attach.restype,ctypes.c_void_p)
        self.assertEqual(self.lib.bpf_link__destroy.argtypes,[ctypes.c_void_p])
        self.assertEqual(result["map_access"]["calls"]["access_mode"],os.O_RDWR)
        self.assertEqual(result["map_access"]["exe_records"]["access_mode"],os.O_RDONLY)

    def test_btf_wrong_hash_size_id_and_lookup_failure_close_once(self):
        for field,value in (("btf_data",b"wrongBTF"),("btf_size",7),("btf_id",74),("btf_ret",-errno.EPERM)):
            with self.subTest(field=field):
                lib=ExecutableLibrary();setattr(lib,field,value)
                # Keep program target fixed while corrupting only resolved BTF id.
                if field=="btf_id":
                    original=lib.info
                    def info(fd,out,length):
                        ret=original(fd,out,length)
                        if fd==lib.programs["capture_exe"]:struct.pack_into("<II",out,220,73,39733)
                        return ret
                    lib.info=info
                with patch("inspect_object.ctypes.CDLL",return_value=lib),patch.object(NativeBackend,"_fd_access",side_effect=lib.fd_access),patch("native_loader.os.close") as close:
                    b=NativeBackend("fake",b"object",profile="executable-online");b.open_object();b.load_object()
                    with self.assertRaises(NativeError):b.verify_loaded()
                    self.assertFalse(b.verified)
                    if field=="btf_ret":close.assert_not_called()
                    else:close.assert_called_once_with(900)

    def test_btf_fd_close_error_is_not_retried(self):
        self.close.side_effect=OSError(errno.EINTR,"close interrupted")
        self.backend.open_object();self.backend.load_object()
        with self.assertRaises(OSError):self.backend.verify_loaded()
        self.close.assert_called_once_with(900)
        self.assertFalse(self.backend.verified)
        self.assertFalse(self.backend.target_btf_verified)

    def test_exact_references_and_lsm_function_reject(self):
        self.backend.open_object();self.backend.load_object()
        self.lib.map_references["call_enter"]=[self.lib.maps["capture_config"]+100]
        with self.assertRaisesRegex(NativeError,"map IDs"):self.backend.verify_loaded()
        self.lib.map_references["call_enter"]=[self.lib.maps[x]+100 for x in EXECUTABLE_MAP_REFS["call_enter"]]
        self.lib.func_id=39734
        with self.assertRaisesRegex(NativeError,"LSM program target"):self.backend.verify_loaded()
        self.close.assert_not_called()

    def test_initial_exe_maps_and_no_task_storage_enumeration(self):
        self.ready();self.lib.calls.clear()
        self.backend.initialize_config(CONFIG_RAW)
        enumerated=[next(n for n,p in self.lib.maps.items() if p==a[0]) for n,a in self.lib.calls if n=="bpf_map_get_next_key"]
        self.assertEqual(set(enumerated),{"connect_records","online_tuples","online_bindings","exe_records"})
        self.assertNotIn("calls",enumerated)
        for name in ("exe_state","exe_records"):
            lib=ExecutableLibrary()
            if name=="exe_state":lib.values[name][bytes(4)]=bytes(63)+b"\1"
            else:lib.values[name][bytes(8)]=bytes(296)
            with patch("inspect_object.ctypes.CDLL",return_value=lib),patch.object(NativeBackend,"_fd_access",side_effect=lib.fd_access):
                b=NativeBackend("fake",b"object",profile="executable-online");b.open_object();b.load_object();b.verify_loaded()
                with self.assertRaises(NativeError):b.initialize_config(CONFIG_RAW)

    def test_five_link_order_exact_targets_and_pointer_destruction(self):
        owner=AdmissionOwner(self.backend,CONFIG_RAW,21,lambda row:None)
        owner.links=[[n,None] for n in exec_core.EXECUTABLE_LINKS]
        owner.start(88)
        names=[n for n,_ in owner.links];self.assertEqual(names,["call_exit","call_enter","capture_exe","hoopa_connect","hoopa_lifecycle"])
        self.assertEqual(len(self.backend.owned_links),5)
        # Legacy admission is unavailable before touching any client socket.
        with self.assertRaisesRegex(ValueError,"links missing"):owner._admit(object())
        with self.assertRaisesRegex(NativeError,"no verified online"):self.backend.online_lookup("1",CONFIG_RAW)
        conn=Mock()
        record=owner.serve_one(SimpleNamespace(accept=lambda:(conn,("127.0.0.1",31001))))
        self.assertEqual(record["status"],"rejected")
        self.assertIsNone(record["grant_monotonic_ns"])
        conn.recv.assert_not_called();conn.send.assert_not_called()
        conn.close.assert_called_once()
        result=owner.close();self.assertEqual(result["errors"],[])
        self.assertEqual(len(self.lib.destroyed),3)
        self.assertEqual(len(set(self.lib.destroyed)),3)
        # BTF temporary + two direct cgroup link FDs; auto links closed only by destroy.
        self.assertEqual([x.args[0] for x in self.close.call_args_list],[900,1,0])
        self.assertEqual(self.backend.link_pointers,{})

    def test_raw_tracepoint_exact_name_and_lsm_wrong_target(self):
        self.ready()
        for name in ("call_enter","call_exit","capture_exe"):
            fd=self.backend.create_link(name,88)
            self.backend.verify_link(fd,name,21)
            if name!="capture_exe":self.lib.name_override=b"wrongname"
            else:self.lib.btf_id=74
            with self.assertRaises(NativeError):self.backend.verify_link(fd,name,21)
            self.lib.name_override=None;self.lib.btf_id=73
            self.backend.close_link(fd)
            with self.assertRaisesRegex(NativeError,"not owned"):self.backend.close_link(fd)

    def test_auto_link_fd_failure_and_destroy_error_not_retried(self):
        self.ready()
        original=self.lib.invoke
        with patch.object(self.lib,"invoke",side_effect=lambda name,*a:-errno.EPERM if name=="bpf_link__fd" else original(name,*a)):
            with self.assertRaises(NativeError):self.backend.create_link("call_enter",88)
        self.assertEqual(len(self.lib.destroyed),1);self.assertFalse(self.backend.owned_links)
        fd=self.backend.create_link("call_exit",88);self.lib.destroy_ret=-errno.EIO
        with self.assertRaises(NativeError):self.backend.close_link(fd)
        with self.assertRaises(NativeError):self.backend.close_link(fd)
        self.assertEqual(len(self.lib.destroyed),2)
        self.close.assert_called_once_with(900)


class ExecutableBootstrapTests(unittest.TestCase):
    def resources(self):
        maps,progs,links=exec_core.resource_names("executable-online")
        return (SimpleNamespace(map_fds={n:i+10 for i,n in enumerate(maps)},program_fds={n:i+30 for i,n in enumerate(progs)}),SimpleNamespace(links=[[n,i+50] for i,n in enumerate(links)]))
    def handoff(self):
        row=handoff();row["schema"]="hoopa-core-identity-handoff-v2";fd=200
        for kind,names in zip(("maps","programs","links"),exec_core.resource_names("executable-online")):
            row[kind]={n:i+fd for i,n in enumerate(names)};fd+=len(names)
        return row
    def test_twenty_cloexec_copies_then_inheritance(self):
        b,o=self.resources();fake=Copies();groups,fds=fake.run(b,o,profile="executable-online")
        self.assertEqual(fds,list(range(200,220)))
        self.assertEqual(len(groups["maps"]),9);self.assertEqual(len(groups["programs"]),6);self.assertEqual(len(groups["links"]),5)
        trace=fake.trace;first=next(i for i,x in enumerate(trace) if x[0]=="inherit")
        self.assertEqual(sum(x[0]=="duplicate" for x in trace[:first]),20)
        for fail in range(20):
            fake=Copies(fail_duplicate=fail)
            with self.assertRaises(OSError):fake.run(b,o,profile="executable-online")
            self.assertEqual(fake.closed,list(reversed(range(200,200+fail))))
        fake=Copies(fail_inherit=219)
        with self.assertRaises(OSError):fake.run(b,o,profile="executable-online")
        self.assertEqual(fake.closed,list(range(219,199,-1)))

    def test_exact_v2_handoff_and_profile_isolation(self):
        row=self.handoff();self.assertEqual(json.loads(exec_core.encode_handoff(row,profile="executable-online")),row)
        with self.assertRaises(ValueError):exec_core.encode_handoff(row)
        with self.assertRaises(ValueError):exec_core.encode_handoff(handoff(),profile="executable-online")
        for kind in ("maps","programs","links"):
            bad=self.handoff();bad[kind].pop(next(iter(bad[kind])))
            with self.assertRaises(ValueError):exec_core.encode_handoff(bad,profile="executable-online")
        bad=self.handoff();bad["links"]["call_enter"]=bad["maps"]["calls"]
        with self.assertRaises(ValueError):exec_core.encode_handoff(bad,profile="executable-online")
        b,o=self.resources()
        with self.assertRaises(ValueError):Copies().run(b,o)
    def test_default_refusal_and_unknown_profile(self):
        with contextlib.redirect_stderr(io.StringIO()),self.assertRaises(SystemExit):exec_core.main([])
        with self.assertRaises(ValueError):exec_core.resource_names("other")
    def test_main_explicit_profile_and_preallocated_slots(self):
        # Reuse host-safe main mocks, then inspect exact pre-start slots and intent.
        case=bootstrap_tests.MainTests()
        with contextlib.ExitStack() as stack:
            owner,close,execute,core,stream=case.main_context(stack,OSError(errno.ENOEXEC,"exec failed"))
            row=self.handoff();groups={n:row[n] for n in ("maps","programs","links")}
            duplicate=stack.enter_context(patch.object(exec_core,"duplicate_resources",return_value=(groups,list(range(200,220)))))
            def started(fd):self.assertEqual(owner.links,[[n,None] for n in exec_core.EXECUTABLE_LINKS]);return {}
            owner.start.side_effect=started
            with self.assertRaisesRegex(OSError,"exec failed"):exec_core.main(arguments()+["--profile","executable-online"])
            duplicate.assert_called_once_with(unittest.mock.ANY,owner,profile="executable-online")
            exported=json.loads(execute.call_args[0][2]["HOOPA_IDENTITY_HANDOFF"])
            self.assertEqual(exported["schema"],"hoopa-core-identity-handoff-v2")
            self.assertEqual(len(exported["maps"])+len(exported["programs"])+len(exported["links"]),20)
            owner.close.assert_called_once()


if __name__=="__main__":unittest.main()
