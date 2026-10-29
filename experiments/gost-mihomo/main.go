// Command hoopa-p0 runs an explicitly requested, isolated calibration experiment.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"text/template"
	"time"
)

//go:embed mihomo.yaml
var mihomoTemplate string

//go:embed gost.yaml
var gostTemplate string

type Options struct {
	Mihomo, MihomoSHA, Gost, GostSHA, Agent, Base, UpstreamProxy string
	Timeout                                                      time.Duration
	KeepCores                                                    bool
	RecorderProbe, RecorderRejectFirst                           bool
	RecorderJournal                                              bool
}
type ConfigValues struct {
	MihomoPort, ControllerPort, HTTPPort, SOCKSPort, MetricsPort               string
	MihomoAuth, HTTPAuth, SOCKSAuth, MetricsAuth                               Credential
	ControllerSecret, ServicePrefix, UpstreamHost, UpstreamPort, DefaultTarget string
	RecorderURL                                                                string
}
type CoreInstance struct {
	Role            string `json:"role"`
	PID             int    `json:"pid"`
	OSStartIdentity string `json:"os_start_identity"`
	BootIdentity    string `json:"boot_identity"`
	BinaryPath      string `json:"binary_path"`
	BinarySHA256    string `json:"binary_sha256"`
	PrivateWorkdir  string `json:"private_workdir"`
	ConfigPath      string `json:"config_path"`
	RunID           string `json:"run_id"`
}
type PrivateEndpoints struct {
	RunID            string         `json:"run_id"`
	MihomoHTTP       string         `json:"mihomo_http_proxy"`
	GostHTTP         string         `json:"gost_http_proxy"`
	GostSOCKS        string         `json:"gost_socks5_proxy"`
	ControllerURL    string         `json:"controller_url"`
	ControllerSecret string         `json:"controller_secret"`
	MetricsURL       string         `json:"metrics_url"`
	MetricsAuth      Credential     `json:"metrics_auth"`
	Cores            []CoreInstance `json:"cores"`
}
type HarnessResult struct {
	RunID      string              `json:"run_id"`
	Directory  string              `json:"directory"`
	ReasonCode string              `json:"reason_code"`
	KeptCores  bool                `json:"kept_cores"`
	Flows      []CalibrationResult `json:"flows"`
	Security   []CheckResult       `json:"security"`
	Counters   CounterDifference   `json:"counters"`
	Gates      []CheckResult       `json:"gates"`
	Observer   *ObserverSummary    `json:"observer,omitempty"`
}

func unverifiedGates() []CheckResult {
	reasons := []string{"original_source_and_terminal_observations_missing", "counter_boundary_not_calibrated", "forward_timing_and_cross_hop_identity_missing", "lifecycle_faults_not_executed", "provider_ordered_routing_not_executed", "sqlite_driver_probes_not_executed", "full_scale_and_security_matrix_not_executed"}
	gates := []CheckResult{}
	for i, reason := range reasons {
		gates = append(gates, CheckResult{fmt.Sprintf("G%02d", i+1), Unverified, reason})
	}
	return gates
}
func main() {
	o := Options{}
	flags := flag.NewFlagSet("hoopa-p0", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&o.Mihomo, "mihomo", "", "absolute fixed Mihomo binary path")
	flags.StringVar(&o.MihomoSHA, "mihomo-sha256", "", "expected lowercase SHA256")
	flags.StringVar(&o.Gost, "gost", "", "absolute fixed GOST binary path")
	flags.StringVar(&o.GostSHA, "gost-sha256", "", "expected lowercase SHA256")
	flags.StringVar(&o.Agent, "agent", "", "optional Agent path: hash and --version only")
	flags.StringVar(&o.Base, "base", os.TempDir(), "existing directory for a new private sandbox")
	flags.StringVar(&o.UpstreamProxy, "upstream-proxy", "", "explicit existing loopback SOCKS5 host:port; never modified")
	flags.DurationVar(&o.Timeout, "timeout", 45*time.Second, "overall experiment bound (maximum 5m)")
	flags.BoolVar(&o.KeepCores, "keep-cores", false, "leave authenticated, identified cores; private endpoint record required")
	flags.BoolVar(&o.RecorderProbe, "recorder-probe", false, "observe native handler recorder HTTP plugin; no gate promotion")
	flags.BoolVar(&o.RecorderRejectFirst, "recorder-reject-first", false, "reject first valid recorder delivery with 503 (requires recorder-probe)")
	flags.BoolVar(&o.RecorderJournal, "recorder-journal", false, "journal delivered recorder events before ACK (requires recorder-probe; Linux)")
	if err := flags.Parse(os.Args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			flags.SetOutput(os.Stdout)
			flags.PrintDefaults()
			return
		}
		json.NewEncoder(os.Stdout).Encode(HarnessResult{ReasonCode: "invalid_argument", Gates: unverifiedGates()})
		os.Exit(2)
	}
	if flags.NArg() != 0 {
		json.NewEncoder(os.Stdout).Encode(HarnessResult{ReasonCode: "unexpected_arguments", Gates: unverifiedGates()})
		os.Exit(2)
	}
	result := runHarness(context.Background(), o)
	json.NewEncoder(os.Stdout).Encode(result)
	if result.ReasonCode != "completed" {
		os.Exit(1)
	}
}

func renderConfigs(v ConfigValues) ([]byte, []byte, error) {
	render := func(name, text string) ([]byte, error) {
		t, err := template.New(name).Option("missingkey=error").Parse(text)
		if err != nil {
			return nil, err
		}
		var b bytes.Buffer
		err = t.Execute(&b, v)
		return b.Bytes(), err
	}
	mh, err := render("mihomo", mihomoTemplate)
	if err != nil {
		return nil, nil, err
	}
	gs, err := render("gost", gostTemplate)
	return mh, gs, err
}
func privateWrite(s *Sandbox, name string, data []byte) error {
	f, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	err = writeSynced(f, data)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("private_write_failed")
	}
	return syncEvidenceDirectory(s.root)
}
func privateJSON(s *Sandbox, name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return privateWrite(s, name, append(data, '\n'))
}
func childEnv(s *Sandbox) []string {
	return []string{"PATH=" + os.Getenv("PATH"), "HOME=" + s.Path(), "XDG_CONFIG_HOME=" + filepath.Join(s.Path(), "config-home"), "XDG_STATE_HOME=" + filepath.Join(s.Path(), "state-home"), "XDG_CACHE_HOME=" + filepath.Join(s.Path(), "cache-home"), "TMPDIR=" + s.Path(), "LANG=C", "LC_ALL=C", "HOOPA_EXPERIMENT_RUN_ID=" + s.RunID()}
}
func binaryHash(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("binary_unavailable")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type versionBuffer struct{ bytes.Buffer }

func (b *versionBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 8192 {
		return 0, errors.New("version_output_limit")
	}
	return b.Buffer.Write(p)
}

var versionPattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9])v?([0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?)\b`)

func extractVersion(role string, output []byte) (string, error) {
	lower := strings.ToLower(string(output))
	if role != "agent" && !strings.Contains(lower, role) {
		return "", errors.New("version_field_missing")
	}
	match := versionPattern.FindSubmatch(output)
	if match == nil {
		return "", errors.New("version_field_missing")
	}
	return string(match[1]), nil
}
func inspectBinary(ctx context.Context, s *Sandbox, role, path, want string) (BinaryRecord, error) {
	if !filepath.IsAbs(path) {
		return BinaryRecord{}, errors.New("absolute_binary_required")
	}
	sum, err := binaryHash(path)
	if err != nil {
		return BinaryRecord{}, errors.New("binary_unavailable")
	}
	if want != "" && sum != want {
		return BinaryRecord{}, errors.New("binary_hash_mismatch")
	}
	arg := "--version"
	if role == "mihomo" {
		arg = "-v"
	}
	if role == "gost" {
		arg = "-V"
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(bounded, path, arg)
	cmd.Dir = s.Path()
	cmd.Env = childEnv(s)
	var output versionBuffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return BinaryRecord{}, errors.New("version_probe_failed")
	}
	version, err := extractVersion(role, output.Bytes())
	if err != nil {
		return BinaryRecord{}, err
	}
	again, err := binaryHash(path)
	if err != nil || again != sum {
		return BinaryRecord{}, errors.New("binary_changed")
	}
	return BinaryRecord{Role: role, Path: path, ReportedVersion: version, SHA256: sum, SourceVerified: false}, nil
}

type ownedCore struct {
	cmd      *exec.Cmd
	done     chan struct{}
	instance CoreInstance
}

func startIdentity(pid int) (string, string, error) {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return "", "", err
	}
	at := strings.LastIndex(string(raw), ")")
	if at < 0 {
		return "", "", errors.New("process_identity_unavailable")
	}
	fields := strings.Fields(string(raw)[at+1:])
	if len(fields) < 20 {
		return "", "", errors.New("process_identity_unavailable")
	}
	start := fields[19]
	if _, err := strconv.ParseUint(start, 10, 64); err != nil {
		return "", "", err
	}
	boot, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", "", err
	}
	return start, strings.TrimSpace(string(boot)), nil
}
func startCore(s *Sandbox, binary BinaryRecord, configPath string, args ...string) (*ownedCore, error) {
	sum, err := binaryHash(binary.Path)
	if err != nil || sum != binary.SHA256 {
		return nil, errors.New("binary_changed")
	}
	log, err := s.root.OpenFile(binary.Role+".log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, errors.New("core_log_unavailable")
	}
	cmd := exec.Command(binary.Path, args...)
	cmd.Dir = s.Path()
	cmd.Env = childEnv(s)
	cmd.Stdout = log
	cmd.Stderr = log
	if err := cmd.Start(); err != nil {
		log.Close()
		return nil, errors.New("core_start_failed")
	}
	core := &ownedCore{cmd: cmd, done: make(chan struct{}), instance: CoreInstance{Role: binary.Role, PID: cmd.Process.Pid, BinaryPath: binary.Path, BinarySHA256: binary.SHA256, PrivateWorkdir: s.Path(), ConfigPath: configPath, RunID: s.RunID()}}
	if runtime.GOOS == "linux" {
		core.instance.OSStartIdentity, core.instance.BootIdentity, _ = startIdentity(cmd.Process.Pid)
	}
	go func() { _ = cmd.Wait(); _ = log.Close(); close(core.done) }()
	return core, nil
}
func (p *ownedCore) stop() bool {
	select {
	case <-p.done:
		return true
	default:
	}
	if err := p.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return false
	}
	select {
	case <-p.done:
		return true
	case <-time.After(3 * time.Second):
		return false
	}
}
func requestRaw(ctx context.Context, address, token string, auth Credential) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return 0, nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if auth.Username != "" {
		req.SetBasicAuth(auth.Username, auth.Password)
	}
	tr := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 2 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 10*1024*1024+1))
	if len(data) > 10*1024*1024 {
		return response.StatusCode, nil, errors.New("evidence_body_limit")
	}
	return response.StatusCode, data, err
}
func awaitCore(ctx context.Context, p *ownedCore, address, token string, auth Credential) error {
	timer := time.NewTimer(12 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		code, _, err := requestRaw(ctx, address, token, auth)
		if err == nil && code == 200 {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("experiment_timeout")
		case <-timer.C:
			return errors.New("core_not_ready")
		case <-p.done:
			return errors.New("core_exited")
		case <-ticker.C:
		}
	}
}
func credential(prefix string) (Credential, error) {
	u, err := NewSecret()
	if err != nil {
		return Credential{}, err
	}
	p, err := NewSecret()
	return Credential{prefix + "_" + u[:16], p}, err
}
func configValues(s *Sandbox, upstream string) (ConfigValues, error) {
	v := ConfigValues{ServicePrefix: s.RunID()[:16], DefaultTarget: "DIRECT"}
	var err error
	fields := []*string{&v.MihomoPort, &v.ControllerPort, &v.HTTPPort, &v.SOCKSPort, &v.MetricsPort}
	for _, field := range fields {
		address, e := s.ReserveTCP()
		if e != nil {
			return v, e
		}
		_, *field, err = net.SplitHostPort(address)
		if err != nil {
			return v, err
		}
	}
	creds := []*Credential{&v.MihomoAuth, &v.HTTPAuth, &v.SOCKSAuth, &v.MetricsAuth}
	for i, c := range creds {
		*c, err = credential(fmt.Sprintf("p%d", i))
		if err != nil {
			return v, err
		}
	}
	v.ControllerSecret, err = NewSecret()
	if err != nil {
		return v, err
	}
	v.UpstreamHost, v.UpstreamPort, err = parseLoopbackProxy(upstream)
	if v.UpstreamHost != "" {
		v.DefaultTarget = "UPSTREAM"
	}
	return v, err
}
func proxySecurity(ctx context.Context, name, target string, proxy *url.URL, expected int) CheckResult {
	tr := proxyTransport(proxy, nil)
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, "GET", target+"/download", nil)
	response, err := client.Do(req)
	if err != nil {
		return CheckResult{"SECURITY", Failed, name + "_request_failed"}
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != expected {
		return CheckResult{"SECURITY", Failed, name + "_status_mismatch"}
	}
	return CheckResult{"SECURITY", Passed, name + "_verified"}
}
func socksSecurity(ctx context.Context, address, target string, auth Credential) CheckResult {
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return CheckResult{"SECURITY", Failed, "socks_unauthorized_connect_failed"}
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write([]byte{5, 1, 0}); err != nil {
		return CheckResult{"SECURITY", Failed, "socks_unauthorized_probe_failed"}
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(conn, reply); err != nil || reply[0] != 5 || reply[1] != 255 {
		return CheckResult{"SECURITY", Failed, "socks_unauthorized_not_rejected"}
	}
	authorized, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return CheckResult{"SECURITY", Failed, "socks_authorized_connect_failed"}
	}
	defer authorized.Close()
	authorized.SetDeadline(time.Now().Add(2 * time.Second))
	if err := socksConnect(authorized, target, auth); err != nil {
		return CheckResult{"SECURITY", Failed, "socks_authorized_handshake_failed"}
	}
	return CheckResult{"SECURITY", Passed, "socks_auth_verified"}
}

func awaitEntry(ctx context.Context, core *ownedCore, address string) error {
	for attempts := 0; attempts < 30; attempts++ {
		conn, err := (&net.Dialer{Timeout: 200 * time.Millisecond}).DialContext(ctx, "tcp", address)
		if err == nil {
			conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("experiment_timeout")
		case <-core.done:
			return errors.New("core_exited")
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("entry_not_ready")
}

func runHarness(parent context.Context, o Options) (result HarnessResult) {
	result = HarnessResult{ReasonCode: "invalid_argument", Flows: []CalibrationResult{}, Security: []CheckResult{}, Gates: unverifiedGates(), Counters: CounterDifference{Status: Unverified, ReasonCode: "counter_observations_missing"}}
	if !filepath.IsAbs(o.Mihomo) || !filepath.IsAbs(o.Gost) || !shaForm.MatchString(o.MihomoSHA) || !shaForm.MatchString(o.GostSHA) || o.Timeout <= 0 || o.Timeout > 5*time.Minute || o.Agent != "" && !filepath.IsAbs(o.Agent) || (o.RecorderRejectFirst || o.RecorderJournal) && !o.RecorderProbe {
		return result
	}
	if _, _, err := parseLoopbackProxy(o.UpstreamProxy); err != nil {
		return result
	}
	ctx, cancel := context.WithTimeout(parent, o.Timeout)
	defer cancel()
	s, err := NewSandbox(o.Base)
	if err != nil {
		result.ReasonCode = "sandbox_unavailable"
		return result
	}
	result.RunID = s.RunID()
	result.Directory = s.Path()
	var evidence *Evidence
	var observer *recorderCollector
	cores := []*ownedCore{}
	leaveRunning := false
	evidenceHealthy := true
	defer func() {
		_ = s.ReleasePorts()
		if observer != nil {
			summary := observer.finish(s)
			result.Observer = &summary
			if result.ReasonCode == "completed" && summary.ReasonCode != "recorder_observation_only" {
				result.ReasonCode = summary.ReasonCode
			}
		}
		if leaveRunning {
			for _, core := range cores {
				if !core.verifyForKeep() {
					leaveRunning = false
					result.ReasonCode = "keep_core_identity_unavailable"
					break
				}
			}
		}
		if !leaveRunning {
			for i := len(cores) - 1; i >= 0; i-- {
				if !cores[i].stop() {
					result.ReasonCode = "owned_core_cleanup_failed"
				}
			}
		}
		result.KeptCores = leaveRunning
		if evidence != nil && evidenceHealthy {
			status := Passed
			if result.ReasonCode != "completed" {
				status = Failed
			}
			if err := evidence.Append("CALIBRATION", "shutdown", status, result.ReasonCode, nil); err != nil {
				result.ReasonCode = "evidence_write_failed"
			}
		}
		if err := privateJSON(s, "result.json", result); err != nil {
			result.ReasonCode = "result_write_failed"
		}
		if evidence != nil {
			_ = evidence.Close()
		}
		_ = s.root.Close()
	}()
	fail := func(reason string) HarnessResult { result.ReasonCode = reason; return result }
	mihomo, err := inspectBinary(ctx, s, "mihomo", o.Mihomo, o.MihomoSHA)
	if err != nil {
		return fail(err.Error())
	}
	gost, err := inspectBinary(ctx, s, "gost", o.Gost, o.GostSHA)
	if err != nil {
		return fail(err.Error())
	}
	binaries := []BinaryRecord{mihomo, gost}
	if o.Agent != "" {
		agent, err := inspectBinary(ctx, s, "agent", o.Agent, "")
		if err != nil {
			return fail(err.Error())
		}
		binaries = append(binaries, agent)
	}
	values, err := configValues(s, o.UpstreamProxy)
	if err != nil {
		return fail("socket_or_credentials_unavailable")
	}
	if o.RecorderProbe {
		observer, values.RecorderURL, err = startRecorderProbe(s, o.RecorderRejectFirst, o.RecorderJournal)
		if err != nil {
			if errors.Is(err, errObserverJournalUnsupported) {
				return fail("recorder_journal_unsupported")
			}
			if o.RecorderJournal {
				return fail("recorder_journal_probe_init_failed")
			}
			return fail("recorder_listener_unavailable")
		}
	}
	mhConfig, gsConfig, err := renderConfigs(values)
	if err != nil {
		return fail("config_render_failed")
	}
	if err := s.root.Mkdir("mihomo-home", 0o700); err != nil {
		return fail("core_home_unavailable")
	}
	if err := privateWrite(s, "mihomo.yaml", mhConfig); err != nil {
		return fail("private_config_write_failed")
	}
	if err := privateWrite(s, "gost.yaml", gsConfig); err != nil {
		return fail("private_config_write_failed")
	}
	digest := sha256.Sum256(append(append(append([]byte{}, mhConfig...), []byte("\n---\n")...), gsConfig...))
	evidence, err = NewEvidence(s, binaries, hex.EncodeToString(digest[:]))
	if err != nil {
		return fail("evidence_init_failed")
	}
	record := func(caseID, kind string, status EvidenceStatus, reason string, refs []string) bool {
		if !evidenceHealthy {
			return false
		}
		if err := evidence.Append(caseID, kind, status, reason, refs); err != nil {
			evidenceHealthy = false
			result.ReasonCode = "evidence_write_failed"
			return false
		}
		return true
	}
	if !record("CALIBRATION", "startup", Unverified, "fixed_binaries_source_unverified", []string{"manifest.json"}) {
		return result
	}
	mhPath := filepath.Join(s.Path(), "mihomo.yaml")
	gsPath := filepath.Join(s.Path(), "gost.yaml")
	home := filepath.Join(s.Path(), "mihomo-home")
	checkLog, err := s.root.OpenFile("mihomo-check.log", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail("core_check_log_failed")
	}
	checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
	check := exec.CommandContext(checkCtx, o.Mihomo, "-t", "-d", home, "-f", mhPath)
	check.Dir = s.Path()
	check.Env = childEnv(s)
	check.Stdout = checkLog
	check.Stderr = checkLog
	checkErr := check.Run()
	checkCancel()
	closeErr := checkLog.Close()
	if checkErr != nil || closeErr != nil {
		return fail("mihomo_config_check_failed")
	}
	if err := s.ReleasePorts(); err != nil {
		return fail("port_reservation_release_failed")
	}
	mh, err := startCore(s, mihomo, mhPath, "-d", home, "-f", mhPath)
	if err != nil {
		return fail(err.Error())
	}
	cores = append(cores, mh)
	controllerURL := "http://127.0.0.1:" + values.ControllerPort
	metricsURL := "http://127.0.0.1:" + values.MetricsPort + "/metrics"
	if err := awaitCore(ctx, mh, controllerURL+"/version", values.ControllerSecret, Credential{}); err != nil {
		return fail(err.Error())
	}
	gs, err := startCore(s, gost, gsPath, "-C", gsPath)
	if err != nil {
		return fail(err.Error())
	}
	cores = append(cores, gs)
	if observer != nil && o.RecorderJournal {
		if err := observer.bindJournalSource(s, gs.instance, []string{values.ServicePrefix + "-http", values.ServicePrefix + "-socks"}); err != nil {
			return fail("recorder_journal_source_bind_failed")
		}
	}
	if err := awaitCore(ctx, gs, metricsURL, "", values.MetricsAuth); err != nil {
		return fail(err.Error())
	}
	mhAddress := "127.0.0.1:" + values.MihomoPort
	httpAddress := "127.0.0.1:" + values.HTTPPort
	socksAddress := "127.0.0.1:" + values.SOCKSPort
	for _, entry := range []struct {
		core    *ownedCore
		address string
	}{{mh, mhAddress}, {gs, httpAddress}, {gs, socksAddress}} {
		if err := awaitEntry(ctx, entry.core, entry.address); err != nil {
			return fail(err.Error())
		}
	}
	targets := httptest.NewUnstartedServer(http.HandlerFunc(calibrationHandler))
	targets.Config.ErrorLog = log.New(io.Discard, "", 0)
	targets.Start()
	defer targets.Close()
	tlsTargets := httptest.NewUnstartedServer(http.HandlerFunc(calibrationHandler))
	tlsTargets.Config.ErrorLog = log.New(io.Discard, "", 0)
	tlsTargets.StartTLS()
	defer tlsTargets.Close()
	roots := x509.NewCertPool()
	roots.AddCert(tlsTargets.Certificate())
	tcpTarget, err := startTCPReference()
	if err != nil {
		return fail("controlled_tcp_unavailable")
	}
	defer tcpTarget.Close()
	paths := []struct {
		name  string
		proxy *url.URL
	}{{"mihomo", proxyURL("http", mhAddress, values.MihomoAuth)}, {"gost-http", proxyURL("http", httpAddress, values.HTTPAuth)}, {"gost-socks", proxyURL("socks5", socksAddress, values.SOCKSAuth)}}
	for _, check := range []struct {
		name, address, token string
		auth                 Credential
		want                 int
	}{{"controller_authorized", controllerURL + "/version", values.ControllerSecret, Credential{}, 200}, {"controller_unauthorized", controllerURL + "/version", "", Credential{}, 401}, {"metrics_authorized", metricsURL, "", values.MetricsAuth, 200}, {"metrics_unauthorized", metricsURL, "", Credential{}, 401}} {
		code, _, err := requestRaw(ctx, check.address, check.token, check.auth)
		status := Passed
		reason := check.name + "_verified"
		if err != nil || code != check.want {
			status = Failed
			reason = check.name + "_not_verified"
		}
		result.Security = append(result.Security, CheckResult{"SECURITY", status, reason})
	}
	result.Security = append(result.Security, proxySecurity(ctx, "mihomo_authorized", targets.URL, paths[0].proxy, 200), proxySecurity(ctx, "mihomo_unauthorized", targets.URL, proxyURL("http", mhAddress, Credential{}), 407), proxySecurity(ctx, "http_authorized", targets.URL, paths[1].proxy, 200), proxySecurity(ctx, "http_unauthorized", targets.URL, proxyURL("http", httpAddress, Credential{}), 407), socksSecurity(ctx, socksAddress, strings.TrimPrefix(targets.URL, "http://"), values.SOCKSAuth))
	securityFailed := false
	for _, check := range result.Security {
		if !record(check.CaseID, "assertion", check.Status, check.ReasonCode, nil) {
			return result
		}
		if check.Status != Passed {
			securityFailed = true
		}
	}
	if securityFailed {
		return fail("security_assertion_failed")
	}
	captureFailed := false
	capture := func(name, address, token string, auth Credential) []byte {
		code, data, err := requestRaw(ctx, address, token, auth)
		status := Passed
		reason := "private_snapshot_captured"
		if err != nil || code != 200 {
			captureFailed = true
			status = Unverified
			reason = "private_snapshot_unavailable"
			data = []byte("{\"status\":\"unverified\",\"reason_code\":\"snapshot_unavailable\"}\n")
		}
		if err := privateWrite(s, name, data); err != nil {
			captureFailed = true
			status = Failed
			reason = "private_snapshot_write_failed"
		}
		if !record("CALIBRATION", "sample", status, reason, []string{name}) {
			captureFailed = true
		}
		return data
	}
	beforeRaw := capture("metrics-before.txt", metricsURL, "", values.MetricsAuth)
	capture("connections-before.json", controllerURL+"/connections", values.ControllerSecret, Credential{})
	for _, path := range paths {
		mid := func() {
			capture("connections-"+path.name+"-mid.json", controllerURL+"/connections", values.ControllerSecret, Credential{})
			capture("metrics-"+path.name+"-mid.txt", metricsURL, "", values.MetricsAuth)
		}
		result.Flows = append(result.Flows, calibrate(ctx, path.name, path.proxy, targets.URL, tlsTargets.URL, roots, mid)...)
		result.Flows = append(result.Flows, calibrateTCP(ctx, path.name, path.proxy, tcpTarget.Addr().String()))
	}
	afterRaw := capture("metrics-after.txt", metricsURL, "", values.MetricsAuth)
	capture("connections-after.json", controllerURL+"/connections", values.ControllerSecret, Credential{})
	// Run ID is a sampling epoch only while this owned GOST process stays alive.
	// It does not cover a core restart or establish G04 recovery semantics.
	before, beforeErr := ParseCounterSnapshot(beforeRaw, s.RunID())
	after, afterErr := ParseCounterSnapshot(afterRaw, s.RunID())
	if beforeErr != nil || afterErr != nil {
		result.Counters = CounterDifference{Status: Unverified, ReasonCode: "counter_precision_or_format_unavailable"}
	} else {
		result.Counters = CompareCounters(before, after)
	}
	failed := false
	for _, flow := range result.Flows {
		if !record("CALIBRATION", "assertion", flow.Status, flow.ReasonCode, nil) {
			return result
		}
		if flow.Status != Passed {
			failed = true
		}
	}
	// No source-PID collector or synchronous cross-hop identity adapter is present.
	result.Gates[0] = CheckIdentityRun(nil, nil, false)
	for _, gate := range result.Gates {
		if !record(gate.CaseID, "assertion", gate.Status, gate.ReasonCode, nil) {
			return result
		}
	}
	if !evidenceHealthy {
		return result
	}
	if captureFailed {
		return fail("evidence_capture_failed")
	}
	result.ReasonCode = "completed"
	if failed {
		result.ReasonCode = "calibration_assertion_failed"
	}
	if o.KeepCores {
		instances := []CoreInstance{}
		for _, core := range cores {
			if !core.verifyForKeep() {
				return fail("keep_core_identity_unavailable")
			}
			instances = append(instances, core.instance)
		}
		endpoints := PrivateEndpoints{s.RunID(), paths[0].proxy.String(), paths[1].proxy.String(), paths[2].proxy.String(), controllerURL, values.ControllerSecret, metricsURL, values.MetricsAuth, instances}
		if err := privateJSON(s, "endpoints.private.json", endpoints); err != nil {
			return fail("private_endpoints_write_failed")
		}
		leaveRunning = true
	}
	return result
}
