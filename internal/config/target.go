package config

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

// Distro is an OpenWrt-derived distribution owlab knows how to boot.
type Distro string

const (
	OpenWrt     Distro = "openwrt"
	ImmortalWrt Distro = "immortalwrt"
)

// DistroSpec is everything owlab needs to know about one distribution.
//
// Adding a fork should be one entry in the table below and nothing else. The
// bar for inclusion is that the project publishes a rootfs TARBALL (or a
// container image) for a target we support — disk images cannot be turned
// into a container.
type DistroSpec struct {
	// Name is the value used in owlab.yaml.
	Name Distro
	// Title is how the distribution calls itself, for messages.
	Title string
	// DownloadHost is the root of the download server, no trailing slash.
	DownloadHost string
	// FilePrefix is the string artifact file names start with, which is not
	// always the same as Name.
	FilePrefix string
	// ImageRepo is the container image repository, or "" when the only way
	// in is unpacking a rootfs tarball.
	//
	// ImmortalWrt deliberately has none even though immortalwrt/rootfs
	// exists: every tag there is labelled linux/amd64, including the ones
	// whose contents are aarch64, so the metadata cannot be used to select
	// an image. The tarball is unambiguous.
	ImageRepo string
	// ReleasesPath and SnapshotPath are the path segments under
	// DownloadHost, before /targets/<target>.
	ReleasesPath string
	SnapshotPath string
	// SnapshotVersion is what appears in artifact names for snapshot builds.
	SnapshotVersion string
	// ForcePackageManager pins the package manager regardless of release
	// number, or "" to derive it from the version the way OpenWrt does.
	//
	// This exists because the version rule is a property of OpenWrt, not of
	// the family: a fork can track OpenWrt 25.12 and still build with opkg
	// (Kwrt does, with `# CONFIG_USE_APK is not set`). Deriving the manager
	// from the release number alone would hand such a router apk commands
	// and fail every install.
	ForcePackageManager PackageManager
}

var distros = map[Distro]DistroSpec{
	OpenWrt: {
		Name: OpenWrt, Title: "OpenWrt",
		DownloadHost: "https://downloads.openwrt.org",
		FilePrefix:   "openwrt",
		ImageRepo:    "openwrt/rootfs",
		ReleasesPath: "releases", SnapshotPath: "snapshots",
		SnapshotVersion: "SNAPSHOT",
	},
	ImmortalWrt: {
		Name: ImmortalWrt, Title: "ImmortalWrt",
		DownloadHost: "https://downloads.immortalwrt.org",
		FilePrefix:   "immortalwrt",
		ImageRepo:    "",
		ReleasesPath: "releases", SnapshotPath: "snapshots",
		SnapshotVersion: "SNAPSHOT",
	},
}

// LookupDistro resolves a distribution name.
func LookupDistro(d Distro) (DistroSpec, bool) {
	s, ok := distros[d]
	return s, ok
}

// KnownDistros lists supported distributions, sorted for stable messages.
func KnownDistros() []string {
	out := make([]string, 0, len(distros))
	for k := range distros {
		out = append(out, string(k))
	}
	sortStrings(out)
	return out
}

// Fidelity is how close to real hardware a router is asked to be. It is
// chosen per router, not globally, because the cheap tier is enough for most
// LuCI work and the expensive ones are not available on every host.
type Fidelity string

// Two tiers, deliberately. An earlier design had a third — a container with
// mac80211_hwsim phys moved in from the HOST kernel — and the VM tier made it
// pointless: a VM has real radios on every host, while the container variant
// needed a Linux kernel the developer controls and could never work on the
// machines that need it most.
const (
	// Basic is a container with procd as PID 1. Works on every host OS.
	Basic Fidelity = "basic"
	// VM is a real OpenWrt kernel under QEMU, run natively on the host.
	VM Fidelity = "vm"
)

// Target describes one OpenWrt build target: how to name it in a tag, where
// its files live on the download server, and how to ask Docker for it.
//
// The Arch/Target split is not cosmetic. OpenWrt publishes both forms as tags
// ("x86-64-25.12.4" and "x86_64-25.12.4" are the same image), but the download
// paths use the target form and the package feeds use the arch form.
type Target struct {
	// Arch is the OpenWrt architecture name, e.g. "aarch64_generic". This is
	// what appears in feed URLs and in the arch-form image tags.
	Arch string
	// Target is the target/subtarget path, e.g. "armsr/armv8". Used to build
	// download URLs.
	Target string
	// TagPrefix is the target-form image tag prefix, e.g. "armsr-armv8".
	TagPrefix string
	// FileSlug is how the target appears inside artifact file names, e.g.
	// "armsr-armv8" in "openwrt-25.12.4-armsr-armv8-rootfs.tar.gz".
	FileSlug string
	// OCIPlatform is what must be passed to `docker --platform`.
	//
	// Only x86_64 normalises to a real OCI platform ("amd64"). Every other
	// target publishes a non-standard architecture string, so a plain pull on
	// an arm64 host fails with "no matching manifest". We therefore always
	// pass --platform explicitly rather than relying on default resolution.
	OCIPlatform string
	// HostPlatform is the honest OCI platform of the binaries inside. Used
	// when we build an image ourselves from a rootfs tarball onto scratch,
	// where we control the metadata and there is no reason to lie.
	HostPlatform string
	// HasRootfsImage says whether upstream publishes a container image for
	// this target. Only 8 targets do; the rest need ImageBuilder.
	HasRootfsImage bool

	// ---- the VM tier ----
	//
	// Set together or not at all. A target qualifies only if upstream
	// publishes a COMBINED disk image for it — kernel, bootloader and rootfs
	// in one file. Targets that publish a bare kernel and a separate rootfs
	// (malta) or that are real boards with no firmware to boot under QEMU
	// (mvebu) are deliberately left out: owlab would have to invent a boot
	// arrangement upstream never tests, and "it boots but not like the real
	// thing" is the one outcome this tier exists to avoid.

	// QEMUSystem is the qemu-system-* binary that boots this target, or ""
	// if the VM tier does not support it.
	QEMUSystem string
	// QEMUMachine is the -machine to boot on.
	QEMUMachine string
	// QEMUCPUEmulated is the -cpu to use when there is no accelerator and
	// every instruction is being translated. Not "max": on TCG the widest
	// model is also the slowest, and this tier is already paying enough.
	QEMUCPUEmulated string
	// Firmware is the firmware file this target needs, or "" when the machine
	// boots the image with the built-in BIOS.
	//
	// armsr publishes EFI-only combined images, so aarch64 and armv7 need an
	// edk2 build present on the host. x86 still publishes a legacy-boot
	// combined image, which SeaBIOS handles with nothing to find.
	Firmware string
	// VMProfile is the image profile in the artifact name ("generic").
	VMProfile string
	// GOARCH is the Go name for this target's CPU, used to decide whether the
	// host can accelerate it or has to translate every instruction.
	GOARCH string
}

// SupportsVM reports whether the VM tier can boot this target.
func (t Target) SupportsVM() bool { return t.QEMUSystem != "" }

// targets is the set owlab supports. It is deliberately limited to what
// upstream actually publishes a rootfs for, because those are the targets
// where `up` can be fast (pull an image) rather than slow (run ImageBuilder).
var targets = map[string]Target{
	"x86_64": {
		Arch: "x86_64", Target: "x86/64",
		TagPrefix: "x86-64", FileSlug: "x86-64",
		OCIPlatform: "linux/amd64", HostPlatform: "linux/amd64",
		HasRootfsImage: true,
		QEMUSystem:     "qemu-system-x86_64", QEMUMachine: "q35",
		QEMUCPUEmulated: "qemu64", VMProfile: "generic", GOARCH: "amd64",
	},
	"aarch64_generic": {
		Arch: "aarch64_generic", Target: "armsr/armv8",
		TagPrefix: "armsr-armv8", FileSlug: "armsr-armv8",
		OCIPlatform: "linux/aarch64_generic", HostPlatform: "linux/arm64",
		HasRootfsImage: true,
		QEMUSystem:     "qemu-system-aarch64", QEMUMachine: "virt",
		QEMUCPUEmulated: "cortex-a72", Firmware: "edk2-aarch64-code.fd",
		VMProfile: "generic", GOARCH: "arm64",
	},
	"arm_cortex-a15_neon-vfpv4": {
		Arch: "arm_cortex-a15_neon-vfpv4", Target: "armsr/armv7",
		TagPrefix: "armsr-armv7", FileSlug: "armsr-armv7",
		OCIPlatform: "linux/arm_cortex-a15_neon-vfpv4", HostPlatform: "linux/arm/v7",
		HasRootfsImage: true,
		QEMUSystem:     "qemu-system-arm", QEMUMachine: "virt",
		QEMUCPUEmulated: "cortex-a15", Firmware: "edk2-arm-code.fd",
		VMProfile: "generic", GOARCH: "arm",
	},
	"arm_cortex-a9_vfpv3-d16": {
		Arch: "arm_cortex-a9_vfpv3-d16", Target: "mvebu/cortexa9",
		TagPrefix: "mvebu-cortexa9", FileSlug: "mvebu-cortexa9",
		OCIPlatform: "linux/arm_cortex-a9_vfpv3-d16", HostPlatform: "linux/arm/v7",
		HasRootfsImage: true,
	},
	"mips_24kc": {
		Arch: "mips_24kc", Target: "malta/be",
		TagPrefix: "malta-be", FileSlug: "malta-be",
		OCIPlatform: "linux/mips_24kc", HostPlatform: "linux/mips",
		HasRootfsImage: true,
	},
	"i386_pentium4": {
		Arch: "i386_pentium4", Target: "x86/generic",
		TagPrefix: "x86-generic", FileSlug: "x86-generic",
		OCIPlatform: "linux/i386_pentium4", HostPlatform: "linux/386",
		HasRootfsImage: true,
		QEMUSystem:     "qemu-system-i386", QEMUMachine: "pc",
		QEMUCPUEmulated: "pentium3", VMProfile: "generic", GOARCH: "386",
	},
}

// LookupTarget resolves an architecture name, or "auto" to match the host.
func LookupTarget(arch string) (Target, error) {
	if arch == "" || arch == "auto" {
		arch = HostArch()
	}
	t, ok := targets[arch]
	if !ok {
		return Target{}, fmt.Errorf("unknown arch %q (known: %s)", arch, strings.Join(KnownArches(), ", "))
	}
	return t, nil
}

// HostArch is the OpenWrt arch that runs natively on this machine.
func HostArch() string {
	switch runtime.GOARCH {
	case "arm64":
		return "aarch64_generic"
	case "amd64":
		return "x86_64"
	case "386":
		return "i386_pentium4"
	case "arm":
		return "arm_cortex-a15_neon-vfpv4"
	default:
		// Emulation is better than refusing to start.
		return "x86_64"
	}
}

// KnownArches lists supported architectures, sorted for stable messages.
func KnownArches() []string {
	out := make([]string, 0, len(targets))
	for k := range targets {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}

// VMArches lists the architectures the VM tier can boot.
func VMArches() []string {
	out := make([]string, 0, len(targets))
	for k, t := range targets {
		if t.SupportsVM() {
			out = append(out, k)
		}
	}
	sortStrings(out)
	return out
}

// PackageManager is apk or opkg, decided by release.
type PackageManager string

const (
	APK  PackageManager = "apk"
	OPKG PackageManager = "opkg"
)

// PackageManagerFor returns the package manager a release ships.
//
// OpenWrt switched from opkg to apk in 25.12; master/SNAPSHOT switched in
// November 2024. Everything at or below 24.10 is opkg. ImmortalWrt tracks
// OpenWrt here, so the same rule applies to both.
func PackageManagerFor(release string) PackageManager {
	if isSnapshot(release) {
		return APK
	}
	major, minor, ok := parseRelease(release)
	if !ok {
		// Unrecognised version strings are assumed modern: a wrong guess
		// toward apk fails loudly at build time, while a wrong guess toward
		// opkg silently produces an image with no working package manager.
		return APK
	}
	if major > 25 || (major == 25 && minor >= 12) {
		return APK
	}
	return OPKG
}

func isSnapshot(release string) bool {
	r := strings.ToLower(release)
	return r == "snapshot" || r == "master" || r == "main" || strings.HasSuffix(r, "-snapshot")
}

func parseRelease(release string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(release, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}
	minor, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}
	return major, minor, true
}

// spec is this router's distribution, falling back to OpenWrt so that a
// validated config never has to nil-check.
func (r *Router) spec() DistroSpec {
	if s, ok := distros[r.Distro]; ok {
		return s
	}
	return distros[OpenWrt]
}

// ReleaseDir is the directory holding one release's artifacts for a target.
func (r *Router) ReleaseDir() string {
	s := r.spec()
	if isSnapshot(r.Release) {
		return fmt.Sprintf("%s/%s/targets/%s", s.DownloadHost, s.SnapshotPath, r.target.Target)
	}
	return fmt.Sprintf("%s/%s/%s/targets/%s", s.DownloadHost, s.ReleasesPath, r.Release, r.target.Target)
}

// RootfsTarballURL is the rootfs tarball for this router's release/target.
//
// This is the base for any distribution with no usable container image, and
// the fallback when someone pins a point release whose images have not been
// published yet.
func (r *Router) RootfsTarballURL() string {
	s := r.spec()
	return fmt.Sprintf("%s/%s-%s-%s-rootfs.tar.gz", r.ReleaseDir(), s.FilePrefix, r.artifactVersion(), r.target.FileSlug)
}

// artifactVersion is the version string that appears inside file names, which
// for a snapshot is the word SNAPSHOT rather than the branch that was asked
// for.
func (r *Router) artifactVersion() string {
	if isSnapshot(r.Release) {
		return r.spec().SnapshotVersion
	}
	return r.Release
}

// VMImageName is the file name of this router's bootable disk image.
//
// squashfs rather than ext4, because squashfs is what a router actually runs:
// a read-only /rom with a writable overlay on top, which is what makes
// firstboot, jffs2reset and the whole "reset to defaults" story behave the way
// they do on hardware. An ext4 image is one flat writable filesystem and
// quietly makes half of that untestable.
func (r *Router) VMImageName() string {
	s := r.spec()
	suffix := "squashfs-combined"
	if r.target.Firmware != "" {
		// armsr publishes EFI-only. There is no non-EFI variant to fall back
		// to, which is why the firmware has to be found on the host.
		suffix += "-efi"
	}
	return fmt.Sprintf("%s-%s-%s-%s-%s.img.gz",
		s.FilePrefix, r.artifactVersion(), r.target.FileSlug, r.target.VMProfile, suffix)
}

// VMImageURL is where to download this router's disk image.
func (r *Router) VMImageURL() string {
	return r.ReleaseDir() + "/" + r.VMImageName()
}

// SumsURL is the checksum file published beside every artifact.
func (r *Router) SumsURL() string { return r.ReleaseDir() + "/sha256sums" }

// BaseImage is the upstream container image for this router, or "" when the
// router must be built from a rootfs tarball instead.
func (r *Router) BaseImage() string {
	// An explicit image wins: it is the whole point of pointing at one.
	if r.Image != "" {
		return r.Image
	}
	s := r.spec()
	if s.ImageRepo == "" || !r.target.HasRootfsImage || r.noUpstreamImage {
		return ""
	}
	if isSnapshot(r.Release) {
		return fmt.Sprintf("%s:%s-master", s.ImageRepo, r.target.Arch)
	}
	return fmt.Sprintf("%s:%s-%s", s.ImageRepo, r.target.Arch, r.Release)
}

// FeedBase is the root under which this release's package feeds live.
//
// The exact point release matters. apk records hard pins in /etc/apk/world
// (base-files=1707~4ccb782af7); pointing a 25.12.1 rootfs at the 25.12.5 feed
// makes every install fail with "breaks: world[...]". Old point releases stay
// available on the download server, so pinning is always possible.
func (r *Router) FeedBase() string {
	s := r.spec()
	if isSnapshot(r.Release) {
		return s.DownloadHost + "/" + s.SnapshotPath
	}
	return fmt.Sprintf("%s/%s/%s", s.DownloadHost, s.ReleasesPath, r.Release)
}

// Title is how this router's distribution calls itself.
func (r *Router) Title() string { return r.spec().Title }

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
