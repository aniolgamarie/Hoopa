"""Scoped profile ABI, freeze/attach ordering and exact handoff; fake libbpf only."""
import contextlib
import ctypes
import errno
import hashlib
import json
import os
import struct
from types import SimpleNamespace
import unittest
from unittest.mock import patch, Mock
import exec_core
import inspect_object
import native_loader as N
from observations import expected_maps,encode_identity_scope,decode_identity_scope,decode_config
from online_admission import AdmissionOwner
from test_exec_core import Copies,handoff,arguments
import test_exec_core as bootstrap_tests
from test_executable_online_profile import ExecutableLibrary
from test_online_profile import CONFIG_RAW
PROFILE='scoped-executable-online'
SCOPE=encode_identity_scope(decode_config(CONFIG_RAW).generation,4026531837,4026531841)

class ScopedLibrary(ExecutableLibrary):
    def __init__(self):
        super().__init__();self.metadata=expected_maps(PROFILE);self.maps={n:20+i for i,n in enumerate(self.metadata)};self.values={n:{} for n in self.maps}
        for n,size in (("capture_config",32),("identity_scope",24),("capture_faults",8),("capture_gate",40),("exe_state",64)):self.values[n][bytes(4)]=bytes(size)
        self.map_references={n:[self.maps[k]+100 for k in sorted(refs)] for n,refs in N.SCOPED_MAP_REFS.items()};self.frozen_maps=set();self.fail_scope_freeze=False;self.scope_writable=False
    def invoke(self,name,*args):
        if name=='bpf_map_freeze':
            self.calls.append((name,args))
            if args[0]==self.maps['identity_scope'] and self.fail_scope_freeze:return -errno.EIO
            if not(args[0]==self.maps['identity_scope'] and self.scope_writable):self.frozen_maps.add(args[0])
            return 0
        if name=='bpf_map_update_elem':
            self.frozen=args[0] in self.frozen_maps
            try:return super().invoke(name,*args)
            finally:self.frozen=False
        return super().invoke(name,*args)

class ScopedNativeTests(unittest.TestCase):
    def setUp(self):
        self.lib=ScopedLibrary()
        for p in (patch('inspect_object.ctypes.CDLL',return_value=self.lib),patch.object(N.NativeBackend,'_fd_access',side_effect=self.lib.fd_access),patch.object(N,'TARGET_BTF_BYTES',len(self.lib.btf_data)),patch.object(N,'TARGET_BTF_SHA256',hashlib.sha256(self.lib.btf_data).hexdigest()),patch('native_loader.os.close')):p.start();self.addCleanup(p.stop)
        self.b=N.NativeBackend('fake',b'object',PROFILE)
    def ready(self):
        self.b.open_object();self.b.load_object();return self.b.verify_loaded()
    def test_exact_new_maps_refs_and_legacy_inventories(self):
        old=expected_maps('executable-online');new=expected_maps(PROFILE);self.assertEqual(set(new)-set(old),{'identity_scope'});self.assertEqual({k:v for k,v in new.items() if k!='identity_scope'},old);self.assertEqual(inspect_object.PROGRAMS[PROFILE],inspect_object.PROGRAMS['executable-online'])
        result=self.ready();self.assertEqual(len(result['maps']),10);self.assertEqual(len(result['programs']),6)
        for n,refs in N.SCOPED_MAP_REFS.items():self.assertEqual(set(result['programs'][n]['map_ids']),{result['maps'][x] for x in refs})
        for n in ('hoopa_lifecycle','hoopa_stop','call_enter','call_exit'):self.assertEqual(N.SCOPED_MAP_REFS[n],N.EXECUTABLE_MAP_REFS[n])
        self.assertNotIn('identity_scope',expected_maps('online'))
    def test_both_configs_written_verified_frozen_before_any_link(self):
        self.b.set_identity_scope(SCOPE);owner=AdmissionOwner(self.b,CONFIG_RAW,21,lambda e:None);owner.links=[[n,None] for n in exec_core.EXECUTABLE_LINKS];owner.start(88)
        self.assertEqual(self.lib.values['identity_scope'][bytes(4)],SCOPE);self.assertEqual(self.lib.frozen_maps,{self.lib.maps['identity_scope'],self.lib.maps['capture_config']})
        calls=self.lib.calls;attach=next(i for i,(n,a) in enumerate(calls) if n in ('bpf_program__attach','bpf_link_create'));freezes=[a[0] for n,a in calls[:attach] if n=='bpf_map_freeze'];self.assertEqual(freezes,[self.lib.maps['capture_config'],self.lib.maps['identity_scope']]);self.assertTrue(self.b.scope_frozen)
        self.assertEqual(self.b.read_frozen_identity_scope(),SCOPE)
        self.lib.values['identity_scope'][bytes(4)]=bytes(24)
        with self.assertRaisesRegex(N.NativeError,'exec scope readback'):self.b.read_frozen_identity_scope()
        owner.close();self.assertFalse(self.b.scope_frozen)
        with self.assertRaises(N.NativeError):self.b.read_frozen_identity_scope()
    def test_missing_or_wrong_scope_and_attach_before_freeze_refused(self):
        self.ready()
        with self.assertRaises(N.NativeError):self.b.initialize_config(CONFIG_RAW)
        for n in exec_core.EXECUTABLE_LINKS:
            with self.assertRaisesRegex(N.NativeError,'both configurations'):self.b.create_link(n,88)
        self.assertFalse(any(n in ('bpf_program__attach','bpf_link_create') for n,a in self.lib.calls))
        with self.assertRaises(N.NativeError):self.b.set_identity_scope(SCOPE)
    def test_generation_nonzero_and_freeze_failures_cleanup_without_attach(self):
        for cause in ('generation','initial','readback','freeze','writable'):
            with self.subTest(cause=cause):
                # Each attempt owns a fresh fake object/backend and no kernel FD.
                lib=ScopedLibrary()
                with patch('inspect_object.ctypes.CDLL',return_value=lib),patch.object(N.NativeBackend,'_fd_access',side_effect=lib.fd_access):
                    b=N.NativeBackend('fake',b'obj',PROFILE);b.set_identity_scope(encode_identity_scope(1,2,3) if cause=='generation' else SCOPE)
                    if cause=='initial':lib.values['identity_scope'][bytes(4)]=b'X'+bytes(23)
                    if cause=='freeze':lib.fail_scope_freeze=True
                    if cause=='writable':lib.scope_writable=True
                    if cause=='readback':
                        orig=b._lookup
                        def lookup(name,key,*a):
                            raw=orig(name,key,*a)
                            return b'X'+raw[1:] if name=='identity_scope' and raw!=bytes(24) else raw
                        b._lookup=lookup
                    o=AdmissionOwner(b,CONFIG_RAW,21,lambda e:None);o.links=[[n,None] for n in exec_core.EXECUTABLE_LINKS]
                    with self.assertRaises(ValueError):o.start(88)
                    self.assertFalse(b.scope_frozen);self.assertFalse(b.owned_links);o.close();self.assertFalse(any(n in ('bpf_program__attach','bpf_link_create') for n,a in lib.calls));self.assertEqual(sum(n=='bpf_object__close' for n,a in lib.calls),1)
    def test_fixed_scope_bytes_and_legacy_cannot_set_scope(self):
        self.assertEqual(decode_identity_scope(SCOPE)['generation'],decode_config(CONFIG_RAW).generation)
        for i in (0,8,12,16,20):
            raw=bytearray(SCOPE)
            if i<16:raw[i:i+(8 if i==0 else 4)]=bytes(8 if i==0 else 4)
            else:raw[i]^=2
            with self.assertRaises(ValueError):decode_identity_scope(bytes(raw))
        for values in ((0,1,1),(1,0,1),(1,1,2**32),(True,1,1)):
            with self.assertRaises(ValueError):encode_identity_scope(*values)
        old=N.NativeBackend('fake',b'obj','executable-online')
        with self.assertRaises(N.NativeError):old.set_identity_scope(SCOPE)

class ScopedBootstrapTests(unittest.TestCase):
    def resources(self):
        maps,progs,links=exec_core.resource_names(PROFILE);return SimpleNamespace(map_fds={n:i+10 for i,n in enumerate(maps)},program_fds={n:i+30 for i,n in enumerate(progs)}),SimpleNamespace(links=[[n,i+50] for i,n in enumerate(links)])
    def record(self):
        row=handoff();row.update(schema='hoopa-core-identity-handoff-v3',userns_inode=4026531837,mountns_inode=4026531841);fd=200
        for k,names in zip(('maps','programs','links'),exec_core.resource_names(PROFILE)):row[k]={n:fd+i for i,n in enumerate(names)};fd+=len(names)
        return row
    def test_exact21_copies_complete_before_inheritance_and_cleanup(self):
        b,o=self.resources();copies=Copies();groups,fds=copies.run(b,o,profile=PROFILE);self.assertEqual(fds,list(range(200,221)));first=next(i for i,x in enumerate(copies.trace) if x[0]=='inherit');self.assertEqual(sum(x[0]=='duplicate' for x in copies.trace[:first]),21)
        for fail in (0,9,20):
            copies=Copies(fail_duplicate=fail)
            with self.assertRaises(OSError):copies.run(b,o,profile=PROFILE)
            self.assertEqual(copies.closed,list(reversed(range(200,200+fail))))
        copies=Copies(fail_inherit=220)
        with self.assertRaises(OSError):copies.run(b,o,profile=PROFILE)
        self.assertEqual(copies.closed,list(range(220,199,-1)))
    def test_exact_v3_fields_old_profiles_unchanged(self):
        row=self.record();self.assertEqual(json.loads(exec_core.encode_handoff(row,PROFILE)),row)
        for profile in ('online','executable-online'):
            with self.assertRaises(ValueError):exec_core.encode_handoff(row,profile)
        for field in ('userns_inode','mountns_inode'):
            for value in (0,True,-1,2**32,'1'):
                bad=self.record();bad[field]=value
                with self.assertRaises(ValueError):exec_core.encode_handoff(bad,PROFILE)
            bad=self.record();del bad[field]
            with self.assertRaises(ValueError):exec_core.encode_handoff(bad,PROFILE)
        for edit in ('unknown','oldschema','missingmap','duplicate'):
            bad=self.record()
            if edit=='unknown':bad['extra']=1
            if edit=='oldschema':bad['schema']='hoopa-core-identity-handoff-v2'
            if edit=='missingmap':del bad['maps']['identity_scope']
            if edit=='duplicate':bad['maps']['identity_scope']=bad['maps']['calls']
            with self.assertRaises(ValueError):exec_core.encode_handoff(bad,PROFILE)
        self.assertEqual(len(exec_core.resource_names('executable-online')[0]),9);self.assertEqual(len(exec_core.resource_names('online')[0]),6)
    def test_main_real_self_pid1_scope_before_owner_start_and_v3(self):
        case=bootstrap_tests.MainTests()
        with contextlib.ExitStack() as stack:
            owner,close,execute,core,stream=case.main_context(stack,OSError(errno.ENOEXEC,'exec failed'));backend=exec_core.NativeBackend.return_value;events=[];backend.set_identity_scope=Mock(side_effect=lambda raw:events.append(('scope',decode_identity_scope(raw))));backend.read_frozen_identity_scope=Mock(return_value=encode_identity_scope(1,4026531836,4026531836))
            row=self.record();groups={n:row[n] for n in ('maps','programs','links')};stack.enter_context(patch.object(exec_core,'duplicate_resources',return_value=(groups,list(range(200,221)))))
            def started(fd):self.assertEqual(owner.links,[[n,None] for n in exec_core.EXECUTABLE_LINKS]);self.assertEqual(len(events),1);events.append(('start',fd));return {}
            owner.start.side_effect=started
            with self.assertRaisesRegex(OSError,'exec failed'):exec_core.main(arguments()+['--profile',PROFILE])
            result=json.loads(execute.call_args[0][2]['HOOPA_IDENTITY_HANDOFF']);self.assertEqual(result['schema'],'hoopa-core-identity-handoff-v3');self.assertEqual(result['userns_inode'],4026531836);self.assertEqual(result['mountns_inode'],4026531836);self.assertEqual(len(result['maps']),10);self.assertEqual(events[0][1]['generation'],int(result['generation']));owner.close.assert_called_once();backend.read_frozen_identity_scope.assert_called_once_with();intent=json.loads(stream.getvalue());self.assertEqual(intent['identity_scope_snapshot']['raw_hex'],backend.read_frozen_identity_scope.return_value.hex())
    def test_exec_scope_lookup_failure_never_execs_and_closes_owned_copies(self):
        with contextlib.ExitStack() as stack:
            owner,close,execute,core,stream=bootstrap_tests.MainTests().main_context(stack)
            backend=exec_core.NativeBackend.return_value
            backend.set_identity_scope=Mock()
            backend.read_frozen_identity_scope=Mock(side_effect=N.NativeError('exec scope readback mismatch'))
            row=self.record();groups={n:row[n] for n in ('maps','programs','links')}
            stack.enter_context(patch.object(exec_core,'duplicate_resources',return_value=(groups,list(range(200,221)))))
            with self.assertRaisesRegex(N.NativeError,'exec scope readback mismatch'):
                exec_core.main(arguments()+['--profile',PROFILE])
            execute.assert_not_called();owner.close.assert_called_once()
            for fd in range(200,221):self.assertEqual(close.call_args_list.count(unittest.mock.call(fd)),1)

if __name__=='__main__':unittest.main()
