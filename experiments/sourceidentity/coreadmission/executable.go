package coreadmission

import (
	"bytes"
	"encoding/hex"
	"errors"
	"strconv"
	"unicode/utf8"
)

// Executable is an immutable value snapshot of the executable observed during a
// connect call. It does not authenticate identity or enable process rules.
type Executable struct {
	Schema               string `json:"schema"`
	Status               string `json:"status"`
	Source               string `json:"source"`
	NameSource           string `json:"name_source"`
	CallToken            string `json:"call_token"`
	ObservedBootNS       string `json:"observed_boot_ns"`
	PathRawHex           string `json:"path_raw_hex"`
	PathText             string `json:"path_text"`
	PathTextValid        bool   `json:"path_text_valid"`
	ProgramNameRawHex    string `json:"program_name_raw_hex"`
	ProgramName          string `json:"program_name"`
	ProgramNameTextValid bool   `json:"program_name_text_valid"`
	RawRecord            string `json:"raw_record"`
}

func executableIdentity(raw []byte, id Identity) (Executable, error) {
	invalid := func() (Executable, error) {
		return Executable{}, errors.New("invalid executable ABI/connector binding")
	}
	if len(raw) != 296 {
		return invalid()
	}
	cookie, err := u64(id.ClientCookie)
	if err != nil {
		return invalid()
	}
	start, err := u64(id.LeaderStartBoottimeNS)
	if err != nil {
		return invalid()
	}
	end, err := u64(id.ObservedBootNS)
	if err != nil {
		return invalid()
	}
	observed, token := le.Uint64(raw[24:]), le.Uint64(raw[32:])
	n := le.Uint32(raw[16:])
	if cookie == 0 || id.TGID == 0 || id.CallerTID == 0 || start == 0 || observed == 0 || token == 0 || le.Uint64(raw) != cookie || le.Uint32(raw[8:]) != id.TGID || le.Uint32(raw[12:]) != id.CallerTID || le.Uint32(raw[20:]) != 1 || observed < start || observed > end || n < 2 || n > 256 {
		return invalid()
	}
	path := raw[40 : 40+int(n)-1]
	if path[0] != '/' || raw[40+int(n)-1] != 0 || bytes.IndexByte(path, 0) >= 0 {
		return invalid()
	}
	name := path[bytes.LastIndexByte(path, '/')+1:]
	if len(name) == 0 {
		return invalid()
	}
	out := Executable{Schema: "sourceidentity-executable-call-v1", Status: "unverified", Source: "task_exe_file_d_path_v1", NameSource: "executable_path_basename_v1", CallToken: strconv.FormatUint(token, 10), ObservedBootNS: strconv.FormatUint(observed, 10), PathRawHex: hex.EncodeToString(path), PathTextValid: utf8.Valid(path), ProgramNameRawHex: hex.EncodeToString(name), ProgramNameTextValid: utf8.Valid(name), RawRecord: hex.EncodeToString(raw)}
	if out.PathTextValid {
		out.PathText = string(path)
	}
	if out.ProgramNameTextValid {
		out.ProgramName = string(name)
	}
	return out, nil
}
