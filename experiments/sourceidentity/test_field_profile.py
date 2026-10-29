"""Explicit v4 handoff selection and fixed trusted-candidate pins; fake OS only."""
import contextlib
import errno
import json
from unittest import TestCase,main
from unittest.mock import Mock,patch
import exec_core as E
import test_exec_core as base
import test_scoped_executable_online_profile as scoped
from observations import encode_identity_scope
PROFILE='scoped-executable-online'
OBJECT='df7a561fd4b97cbb03b1f7083e3ffcc54a465233ae06ee47c4be67989eb8fc49'
LIB='1c928f61158f25d39cd3fbc7ddafeebec2448886225e2c1824455fcfcd15d04c'
class FieldProfileTests(TestCase):
    def row(self):
        h=scoped.ScopedBootstrapTests().record();h.update(schema='hoopa-core-identity-handoff-v4',identity_profile=E.IDENTITY_PROFILE);return h
    def argv(self):
        args=base.arguments();args[args.index('--object-sha256')+1]=OBJECT;args[args.index('--libbpf-sha256')+1]=LIB
        return args+['--profile',PROFILE,'--identity-profile',E.IDENTITY_PROFILE]
    def test_exact_v4_and_old_shapes(self):
        row=self.row();self.assertEqual(json.loads(E.encode_handoff(row,PROFILE,E.IDENTITY_PROFILE)),row)
        for old in ('online','executable-online',PROFILE):
            with self.assertRaises(ValueError):E.encode_handoff(row,old)
        for name,value in (('schema','hoopa-core-identity-handoff-v3'),('identity_profile','other')):
            bad=dict(row);bad[name]=value
            with self.assertRaises(ValueError):E.encode_handoff(bad,PROFILE,E.IDENTITY_PROFILE)
        bad=dict(row);del bad['identity_profile']
        with self.assertRaises(ValueError):E.encode_handoff(bad,PROFILE,E.IDENTITY_PROFILE)
        with self.assertRaises(ValueError):E.encode_handoff(row,'executable-online',E.IDENTITY_PROFILE)
    def context(self,stack):
        values=base.MainTests().main_context(stack,OSError(errno.ENOEXEC,'exec failed'));backend=E.NativeBackend.return_value;backend.set_identity_scope=Mock();backend.read_frozen_identity_scope=Mock(return_value=encode_identity_scope(2**64-1,4026531836,4026531836));row=self.row();stack.enter_context(patch.object(E,'duplicate_resources',return_value=({n:row[n] for n in ('maps','programs','links')},list(range(200,221)))));stack.enter_context(patch.object(E.platform,'release',return_value='6.18.54.1-microsoft-standard-WSL2'));stack.enter_context(patch.object(E.platform,'machine',return_value='x86_64'));stack.enter_context(patch.dict(E.os.environ,HOOPA_TRACE_PROFILE='5'));return values
    def test_main_fixed_assets_and_actual_fd_exec(self):
        with contextlib.ExitStack() as stack:
            owner,close,execute,core,stream=self.context(stack)
            with self.assertRaisesRegex(OSError,'exec failed'):E.main(self.argv())
            row=json.loads(execute.call_args[0][2]['HOOPA_IDENTITY_HANDOFF']);self.assertEqual(row['schema'],'hoopa-core-identity-handoff-v4');self.assertEqual(row['identity_profile'],E.IDENTITY_PROFILE);self.assertEqual(len(row['maps']),10);self.assertEqual(execute.call_args[0][0],89)
            E.pinned.assert_any_call('/sys/kernel/btf/vmlinux','1c1326be5d6ca0e60ad8790971b6765bba91cc8a3ab897a94794177484c8c9b9',8*1024*1024);self.assertEqual(json.loads(stream.getvalue())['identity_scope_snapshot']['raw_hex'],E.NativeBackend.return_value.read_frozen_identity_scope.return_value.hex());owner.close.assert_called_once()
    def test_failed_fixed_asset_never_opens_core_or_loads(self):
        for what in ('object','library','release','machine','trace','profile'):
            with self.subTest(what=what),contextlib.ExitStack() as stack:
                owner,close,execute,core,stream=self.context(stack);args=self.argv()
                if what=='object':args[args.index('--object-sha256')+1]='a'*64
                if what=='library':args[args.index('--libbpf-sha256')+1]='b'*64
                if what=='release':E.platform.release.return_value='other'
                if what=='machine':E.platform.machine.return_value='aarch64'
                if what=='trace':E.os.environ['HOOPA_TRACE_PROFILE']='4'
                if what=='profile':args[args.index('--profile')+1]='executable-online'
                with self.assertRaises(ValueError):E.main(args)
                core.assert_not_called();E.NativeBackend.assert_not_called();owner.start.assert_not_called();execute.assert_not_called()
if __name__=='__main__':main()
