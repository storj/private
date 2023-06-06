// Copyright (C) 2019 Storj Labs, Inc.
// See LICENSE for copying information.

package version

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/blang/semver"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"storj.io/common/pb"
	"storj.io/common/storj"
)

const quote = byte('"')

var (
	// VerError is the error class for version-related errors.
	VerError = errs.Class("version")

	// the following fields are set by linker flags. if any of them
	// are set and fail to parse, the program will fail to start.
	buildTimestamp  string // unix seconds since epoch
	buildCommitHash string
	buildVersion    string // semantic version format
	buildRelease    string // true/false

	// Build is a struct containing all relevant build information associated with the binary.
	Build Info
)

// Info is the versioning information for a binary.
type Info struct {
	// sync/atomic cache
	commitHashCRC uint32

	Timestamp  time.Time `json:"timestamp,omitempty"`
	CommitHash string    `json:"commitHash,omitempty"`
	Version    SemVer    `json:"version"`
	Release    bool      `json:"release,omitempty"`
	Modified   bool      `json:"modified,omitempty"`
}

// SemVer represents a semantic version.
// TODO: replace with semver.Version.
type SemVer struct {
	semver.Version
}

// OldSemVer represents a semantic version.
//
// Note: use `SemVer` in newer code; these structs marshal to JSON differently.
type OldSemVer struct {
	Major int64 `json:"major"`
	Minor int64 `json:"minor"`
	Patch int64 `json:"patch"`
}

// AllowedVersions provides the Minimum SemVer per Service.
// TODO: I don't think this name is representative of what this struct now holds.
type AllowedVersions struct {
	Satellite   OldSemVer
	Storagenode OldSemVer
	Uplink      OldSemVer
	Gateway     OldSemVer
	Identity    OldSemVer

	Processes Processes `json:"processes"`
}

// Processes describes versions for each binary.
// TODO: this name is inconsistent with the versioncontrol server pkg's analogue, `Versions`.
type Processes struct {
	Satellite          Process `json:"satellite"`
	Storagenode        Process `json:"storagenode"`
	StoragenodeUpdater Process `json:"storagenode-updater"`
	Uplink             Process `json:"uplink"`
	Gateway            Process `json:"gateway"`
	Identity           Process `json:"identity"`
}

// Process versions for specific binary.
type Process struct {
	Minimum   Version `json:"minimum"`
	Suggested Version `json:"suggested"`
	Rollout   Rollout `json:"rollout"`
}

// Version represents version and download URL for binary.
type Version struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// Rollout represents the state of a version rollout.
type Rollout struct {
	Seed   RolloutBytes `json:"seed"`
	Cursor RolloutBytes `json:"cursor"`
}

// RolloutBytes implements json un/marshalling using hex de/encoding.
type RolloutBytes [32]byte

// MarshalJSON hex-encodes RolloutBytes and pre/appends JSON string literal quotes.
func (rb RolloutBytes) MarshalJSON() ([]byte, error) {
	zeroRolloutBytes := RolloutBytes{}
	if bytes.Equal(rb[:], zeroRolloutBytes[:]) {
		return []byte{quote, quote}, nil
	}

	hexBytes := make([]byte, hex.EncodedLen(len(rb)))
	hex.Encode(hexBytes, rb[:])
	encoded := append([]byte{quote}, hexBytes...)
	encoded = append(encoded, quote)
	return encoded, nil
}

// UnmarshalJSON drops the JSON string literal quotes and hex-decodes RolloutBytes .
func (rb *RolloutBytes) UnmarshalJSON(b []byte) error {
	if _, err := hex.Decode(rb[:], b[1:len(b)-1]); err != nil {
		return VerError.Wrap(err)
	}
	return nil
}

// NewSemVer parses a given version and returns an instance of SemVer or
// an error if unable to parse the version.
func NewSemVer(v string) (SemVer, error) {
	ver, err := semver.ParseTolerant(v)
	if err != nil {
		return SemVer{}, VerError.Wrap(err)
	}

	return SemVer{
		Version: ver,
	}, nil
}

// NewOldSemVer parses a given version and returns an instance of OldSemVer or
// an error if unable to parse the version.
func NewOldSemVer(v string) (OldSemVer, error) {
	ver, err := NewSemVer(v)
	if err != nil {
		return OldSemVer{}, err
	}

	return OldSemVer{
		Major: int64(ver.Major),
		Minor: int64(ver.Minor),
		Patch: int64(ver.Patch),
	}, nil
}

// Compare compare two versions, return -1 if compared version is greater, 0 if equal and 1 if less.
func (sem *SemVer) Compare(version SemVer) int {
	return sem.Version.Compare(version.Version)
}

// String converts the SemVer struct to a more easy to handle string.
func (sem *SemVer) String() (version string) {
	base := fmt.Sprintf("v%d.%d.%d", sem.Major, sem.Minor, sem.Patch)
	if len(sem.Pre) > 0 {
		var build string
		for _, val := range sem.Pre {
			build = build + "-" + val.String()
		}
		return fmt.Sprintf("%s%s", base, build)
	}
	return base
}

// IsZero checks if the semantic version is its zero value.
func (sem SemVer) IsZero() bool {
	return reflect.ValueOf(sem).IsZero()
}

func (old OldSemVer) String() string {
	return fmt.Sprintf("v%d.%d.%d", old.Major, old.Minor, old.Patch)
}

// SemVer converts a version struct into a semantic version struct.
func (ver *Version) SemVer() (SemVer, error) {
	return NewSemVer(ver.Version)
}

// IsZero checks if the Version is its zero value.
func (ver *Version) IsZero() bool {
	return reflect.ValueOf(ver.Version).IsZero()
}

// New creates Version_Info from a json byte array.
func New(data []byte) (v Info, err error) {
	err = json.Unmarshal(data, &v)
	return v, VerError.Wrap(err)
}

// IsZero checks if the version struct is its zero value.
func (info Info) IsZero() bool {
	return reflect.ValueOf(info).IsZero()
}

// Marshal converts the existing Version Info to any json byte array.
func (info Info) Marshal() ([]byte, error) {
	data, err := json.Marshal(info)
	if err != nil {
		return nil, VerError.Wrap(err)
	}
	return data, nil
}

// Proto converts an Info struct to a pb.NodeVersion
// TODO: shouldn't we just use pb.NodeVersion everywhere? gogoproto will let
// us make it match Info.
func (info Info) Proto() (*pb.NodeVersion, error) {
	return &pb.NodeVersion{
		Version:    info.Version.String(),
		CommitHash: info.CommitHash,
		Timestamp:  info.Timestamp,
		Release:    info.Release,
	}, nil
}

// String returns with new line separated, printable information for humans.
func (info Info) String() (out string) {
	if info.Release {
		out += fmt.Sprintln("Release build")
	} else {
		out += fmt.Sprintln("Development build")
	}

	if !info.Version.IsZero() {
		out += fmt.Sprintln("Version:", info.Version.String())
	}
	if !info.Timestamp.IsZero() {
		out += fmt.Sprintln("Build timestamp:", info.Timestamp.Format(time.RFC822))
	}
	if info.CommitHash != "" {
		out += fmt.Sprintln("Git commit:", info.CommitHash)
	}
	if info.Modified {
		out += fmt.Sprintln("Modified (dirty): true")
	}
	return out
}

// Log prints out the version information to a zap compatible log.
func (info Info) Log(logger func(msg string, fields ...zap.Field)) {
	logger("Version info",
		zap.Stringer("Version", info.Version.Version),
		zap.String("Commit Hash", info.CommitHash),
		zap.Stringer("Build Timestamp", info.Timestamp),
		zap.Bool("Release Build", info.Release),
		zap.Bool("Modified", info.Modified))
}

// PercentageToCursorF calculates the cursor value for the given floating point percentage.
func PercentageToCursorF(pct float64) RolloutBytes {
	// NB: convert the max value to a number, multiply by the percentage, convert back.
	var maxInt, maskInt big.Int
	var maxBytes RolloutBytes
	for i := 0; i < len(maxBytes); i++ {
		maxBytes[i] = 255
	}
	maxInt.SetBytes(maxBytes[:])
	maskInt.Mul(maskInt.Div(&maxInt, big.NewInt(100*10000)), big.NewInt(int64(pct*10000)))
	var cursor RolloutBytes
	copy(cursor[:], maskInt.Bytes())

	return cursor
}

// PercentageToCursor calculates the cursor value for the given percentage of nodes which should update.
// Deprecated: use PercentageToCursorF which is more precise.
func PercentageToCursor(pct int) RolloutBytes {
	// NB: convert the max value to a number, multiply by the percentage, convert back.
	var maxInt, maskInt big.Int
	var maxBytes RolloutBytes
	for i := 0; i < len(maxBytes); i++ {
		maxBytes[i] = 255
	}
	maxInt.SetBytes(maxBytes[:])
	maskInt.Div(maskInt.Mul(&maxInt, big.NewInt(int64(pct))), big.NewInt(100))

	var cursor RolloutBytes
	copy(cursor[:], maskInt.Bytes())

	return cursor
}

// ShouldUpdate checks if for the the given rollout state, a user with the given nodeID should update.
// Deprecated and should eventually be unexported. Please use ShouldUpdateVersion instead.
func ShouldUpdate(rollout Rollout, nodeID storj.NodeID) bool {
	return isRolloutCandidate(nodeID, rollout)
}

func isRolloutCandidate(nodeID storj.NodeID, rollout Rollout) bool {
	hash := hmac.New(sha256.New, rollout.Seed[:])
	_, err := hash.Write(nodeID[:])
	if err != nil {
		panic(err)
	}
	return bytes.Compare(hash.Sum(nil), rollout.Cursor[:]) <= 0
}

// ShouldUpdateVersion determines if, given a current version and data from the version server, if
// the current version should be updated. It returns the Version to update to or an empty Version.
func ShouldUpdateVersion(currentVersion SemVer, nodeID storj.NodeID, requested Process) (updateVersion Version, reason string, err error) {
	// first, confirm if an update is even necessary
	suggestedVersion, err := requested.Suggested.SemVer()
	if err != nil {
		return Version{}, "", err
	}
	if currentVersion.Compare(suggestedVersion) >= 0 {
		return Version{}, "Version is up to date", nil
	}

	// next, make sure we're at least running the minimum version. See
	// https://github.com/storj/storj/pull/2677#pullrequestreview-270882629
	// and storj/docs/blueprints/storage-node-automatic-updater.md
	minimumVersion, err := requested.Minimum.SemVer()
	if err != nil {
		return Version{}, "", err
	}
	if currentVersion.Compare(minimumVersion) < 0 {
		return requested.Minimum, "Version is below minimum allowed", nil
	}

	// Okay, now consider the rollout
	rollout := isRolloutCandidate(nodeID, requested.Rollout)
	if rollout {
		return requested.Suggested, "New version is being rolled out and this node is a candidate", nil
	}

	return Version{}, "New version is being rolled out but hasn't made it to this node yet", nil
}

func getInfoFromBuildTags() Info {
	if buildVersion == "" && buildTimestamp == "" && buildCommitHash == "" && buildRelease == "" {
		return Info{}
	}
	timestamp, err := strconv.ParseInt(buildTimestamp, 10, 64)
	if err != nil {
		panic(VerError.Wrap(err))
	}
	info := Info{
		Timestamp:  time.Unix(timestamp, 0),
		CommitHash: buildCommitHash,
		Release:    strings.ToLower(buildRelease) == "true",
		Modified:   strings.Contains(buildCommitHash, "dirty"),
	}

	sv, err := NewSemVer(buildVersion)
	if err != nil {
		panic(err)
	}

	info.Version = sv

	if Build.Timestamp.Unix() == 0 || Build.CommitHash == "" {
		Build.Release = false
	}
	return info
}
