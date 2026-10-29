//go:build linux && amd64

package control

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"reflect"
	"strconv"
	"time"

	"hoopa/internal/config"
	"hoopa/internal/mihomo"
	ownedruntime "hoopa/internal/runtime"
	profile "hoopa/protocol/sourceprofile"
)

var (
	ErrAppNotFound             = errors.New("APP_NOT_FOUND")
	ErrProfileUnavailable      = errors.New("PROFILE_UNAVAILABLE")
	ErrManagedEntryUnavailable = errors.New("MANAGED_ENTRY_UNAVAILABLE")
	ErrAppMeteringMode         = errors.New("METERING_MODE_INVALID")
	ErrAppMeteringUnsupported  = errors.New("METERING_UNSUPPORTED")
)

// ApplicationCapture owns a configuration snapshot. It is not runtime proof,
// a launch ticket, or authority to start/reconfigure any process.
type ApplicationCapture struct{ data *applicationCaptureData }
type applicationCaptureData struct {
	profile    config.AppLaunchProfile
	app        config.App
	binding    *config.AppBinding
	base       config.ActivePointer
	capability mihomo.CapabilityRecord
	configSHA  string
}

func (ApplicationCapture) Format(s fmt.State, _ rune) {
	_, _ = io.WriteString(s, "control.ApplicationCapture{private material}")
}
func (ApplicationCapture) MarshalJSON() ([]byte, error) {
	return []byte(`"private application capture"`), nil
}
func cloneLaunchApp(a config.App) config.App { a.Bypass = append([]string{}, a.Bypass...); return a }
func (c ApplicationCapture) Profile() (config.AppLaunchProfile, error) {
	if c.data == nil {
		return config.AppLaunchProfile{}, ErrProfileUnavailable
	}
	p := c.data.profile
	p.App = cloneLaunchApp(p.App)
	if p.Generation != nil {
		g := *p.Generation
		p.Generation = &g
	}
	if p.ManifestSHA256 != nil {
		h := *p.ManifestSHA256
		p.ManifestSHA256 = &h
	}
	return p, nil
}
func (c ApplicationCapture) Application() (config.App, error) {
	if c.data == nil {
		return config.App{}, ErrProfileUnavailable
	}
	return cloneLaunchApp(c.data.app), nil
}
func (c ApplicationCapture) SensitiveBinding() (*config.AppBinding, error) {
	if c.data == nil {
		return nil, ErrProfileUnavailable
	}
	if c.data.binding == nil {
		return nil, nil
	}
	b := *c.data.binding
	return &b, nil
}

// CaptureApplication is read-only, including for missing or invalid state. An
// existing committed generation always takes precedence over editable draft.
func CaptureApplication(ctx context.Context, paths config.Paths, id config.ID, mode string, meter bool) (out ApplicationCapture, err error) {
	if ctx == nil {
		return out, ErrRequest
	}
	if _, e := config.ParseID(string(id)); e != nil || mode != "" && mode != "managed" && mode != "inherit" && mode != "direct" {
		return out, ErrRequest
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	store, e := config.OpenStore(paths, false)
	if e != nil {
		return out, ErrProfileUnavailable
	}
	defer func() {
		if store.Close() != nil && err == nil {
			out = ApplicationCapture{}
			err = ErrProfileUnavailable
		}
	}()
	base, e := store.ReadActive()
	if e != nil {
		return out, ErrProfileUnavailable
	}
	data := &applicationCaptureData{base: base}
	var raw []byte
	var bundle config.StoredBundle
	parseMode := config.IntentDraft
	if base.Generation == nil {
		var sha string
		raw, sha, e = store.ReadDraft()
		if e != nil {
			return out, ErrProfileUnavailable
		}
		data.profile = config.AppLaunchProfile{Kind: "draft", Revision: "draft:" + sha, IntentSHA256: sha}
	} else {
		bundle, e = store.ReadBundle(*base.Generation)
		if e != nil || bundle.ManifestSHA256 != base.ManifestSHA256 {
			return out, ErrProfileUnavailable
		}
		for _, file := range bundle.Manifest.Files {
			if file.Kind == "intent" {
				if raw != nil {
					return out, ErrProfileUnavailable
				}
				raw = bundle.Files[file.Name]
			}
		}
		canonical, e := config.CanonicalJSON(raw)
		if e != nil {
			return out, ErrProfileUnavailable
		}
		raw = canonical
		g, h := *base.Generation, base.ManifestSHA256
		data.profile = config.AppLaunchProfile{Kind: "committed", Revision: string(base.Revision), IntentSHA256: hash(raw), Generation: &g, ManifestSHA256: &h}
		parseMode = config.IntentApply
	}
	intent, e := config.ParseIntent(raw, parseMode)
	if e != nil {
		return out, ErrProfileUnavailable
	}
	found := false
	for _, app := range intent.Apps {
		if app.ID == id {
			data.profile.App = cloneLaunchApp(app)
			data.app = cloneLaunchApp(app)
			found = true
			break
		}
	}
	if !found {
		return out, ErrAppNotFound
	}
	if mode != "" {
		data.app.Mode = mode
	}
	data.app.Metering = data.app.Metering || meter
	if data.app.Metering {
		if data.app.Mode != "managed" {
			return out, ErrAppMeteringMode
		}
		return out, ErrAppMeteringUnsupported
	}
	if data.app.Mode == "managed" {
		if base.Generation == nil {
			return out, ErrManagedEntryUnavailable
		}
		if e = captureManagedApplication(store, bundle, data); e != nil {
			return out, ErrManagedEntryUnavailable
		}
	}
	if ctx.Err() != nil {
		return out, ctx.Err()
	}
	return ApplicationCapture{data}, nil
}

func captureManagedApplication(store *config.Store, bundle config.StoredBundle, data *applicationCaptureData) error {
	impact, e := impactFromBundle(bundle.Manifest, bundle.Files)
	if e != nil || !impact.Known {
		return ErrManagedEntryUnavailable
	}
	cap, e := mihomo.ApprovedCapability(bundle.Manifest.Binaries[0])
	if e != nil || cap.Mode != "basic-app-routing" {
		return ErrManagedEntryUnavailable
	}
	name, e := config.AppBindingFilename(data.app.ID)
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	var bindingRaw, generatedRaw []byte
	for _, file := range bundle.Manifest.Files {
		if file.Kind == "app_binding" && file.Name == name {
			bindingRaw = bundle.Files[file.Name]
		}
		if file.Kind == "generated_config" {
			generatedRaw = bundle.Files[file.Name]
		}
	}
	binding, e := config.DecodeAppBinding(bindingRaw)
	if e != nil || binding.AppID() != data.app.ID {
		return ErrManagedEntryUnavailable
	}
	current, e := store.ReadAppBinding(data.app.ID)
	if e != nil || current.Digest() != binding.Digest() {
		return ErrManagedEntryUnavailable
	}
	settingsRaw, _, e := store.ReadSourceSettings()
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	settings, e := DecodeSourceSettings(settingsRaw)
	if e != nil || settings.Digest() != binding.SettingsSHA256() {
		return ErrManagedEntryUnavailable
	}
	runtime, e := settings.Runtime()
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	generated, e := profile.Validate(generatedRaw, string(bundle.Manifest.Generation))
	if e != nil || generated.Controller != runtime.Controller || generated.Secret != runtime.Secret || !reflect.DeepEqual(generated.Listeners[0], runtime.Entry) {
		return ErrManagedEntryUnavailable
	}
	entry, e := binding.SensitiveEntry()
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	found := false
	for _, listener := range generated.Listeners[1:] {
		if listener.Name == entry.Name {
			if found || !reflect.DeepEqual(listener, entry) {
				return ErrManagedEntryUnavailable
			}
			found = true
		}
	}
	if !found {
		return ErrManagedEntryUnavailable
	}
	data.binding = &binding
	data.capability = cap
	data.configSHA = hash(generatedRaw)
	return nil
}

// QualifyApplicationEntry requires a fresh status from the authenticated local
// management channel. It then independently verifies the original core and the
// owner of the EXACT entry connection before transmitting any credentials.
// It performs no CONNECT, reload, lease allocation, service start or repair.
func QualifyApplicationEntry(ctx context.Context, paths config.Paths, capture ApplicationCapture, status ServiceStatus) error {
	if ctx == nil || capture.data == nil || capture.data.binding == nil {
		return ErrManagedEntryUnavailable
	}
	d := capture.data
	if d.app.Mode != "managed" || status.Service != "running" || status.Core.State != "verified-running" || status.Core.Reason != "fresh_original_proof" || status.Core.RunID == nil || status.Core.PID == nil || status.Core.StartTicks == nil || status.Core.BootID == nil || status.Generation == nil || d.base.Generation == nil || *status.Generation != *d.base.Generation || status.Revision != d.base.Revision || status.DesiredRuntime != "running" || status.CommittedRuntime != "running" {
		return ErrManagedEntryUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	store, e := config.OpenStore(paths, false)
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	defer store.Close()
	checkActive := func() bool { current, e := store.ReadActive(); return e == nil && reflect.DeepEqual(current, d.base) }
	if !checkActive() {
		return ErrManagedEntryUnavailable
	}
	intent, _, e := store.ReadRunIntent(*status.Core.RunID)
	if e != nil || intent.Generation != *d.base.Generation || intent.ManifestSHA256 != d.base.ManifestSHA256 || intent.ConfigSHA256 != d.configSHA || intent.Binary != d.capability.Binary {
		return ErrManagedEntryUnavailable
	}
	controller, e := mihomo.NewController(store, d.capability)
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	ownership, e := ownedruntime.NewOwnership(store, controller)
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	run, e := ownership.Observe(ctx, *status.Core.RunID)
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	defer run.Close()
	identity := run.Identity()
	if identity.PID != *status.Core.PID || identity.StartTicks != *status.Core.StartTicks || identity.BootID != *status.Core.BootID || run.Observation().Gate != "open" {
		return ErrManagedEntryUnavailable
	}
	entry, e := d.binding.SensitiveEntry()
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	conn, e := run.DialOwnedTCP(ctx, net.JoinHostPort(entry.Listen, strconv.Itoa(int(entry.Port))))
	if e != nil {
		return ErrManagedEntryUnavailable
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if conn.SetDeadline(deadline) != nil || authenticateApplicationEntry(conn, entry.Users[0]) != nil || ctx.Err() != nil || !checkActive() {
		return ErrManagedEntryUnavailable
	}
	return nil
}

func authenticateApplicationEntry(conn io.ReadWriter, user profile.User) error {
	if len(user.Username) < 1 || len(user.Username) > 255 || len(user.Password) < 1 || len(user.Password) > 255 {
		return ErrManagedEntryUnavailable
	}
	write := func(raw []byte) bool { n, e := conn.Write(raw); return e == nil && n == len(raw) }
	var response [2]byte
	if !write([]byte{5, 1, 2}) {
		return ErrManagedEntryUnavailable
	}
	if _, e := io.ReadFull(conn, response[:]); e != nil || !bytes.Equal(response[:], []byte{5, 2}) {
		return ErrManagedEntryUnavailable
	}
	raw := append([]byte{1, byte(len(user.Username))}, user.Username...)
	raw = append(raw, byte(len(user.Password)))
	raw = append(raw, user.Password...)
	if !write(raw) {
		return ErrManagedEntryUnavailable
	}
	if _, e := io.ReadFull(conn, response[:]); e != nil || !bytes.Equal(response[:], []byte{1, 0}) {
		return ErrManagedEntryUnavailable
	}
	return nil
}
