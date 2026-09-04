# A dev "router": a stock OpenWrt-family userland with procd as PID 1.
#
# What runs here is the release's own rootfs, not a hand-assembled
# approximation — procd, netifd, ubus, rpcd, uhttpd, all of it. That matters
# more than it sounds: LuCI's dispatcher reads board data from procd over
# ubus, and running uhttpd directly instead of the init system makes LuCI
# crash with "left-hand side expression is null" (openwrt/luci#8726). There is
# no shortcut here; the init system is the product.
#
# Two ways in, selected by BASE_STAGE:
#
#   upstream  FROM openwrt/rootfs:<arch>-<version>. Fast, and the only images
#             upstream publishes are for the 8 targets that build a rootfs
#             tarball.
#   tarball   unpack ROOTFS_URL onto scratch. Needed for ImmortalWrt — every
#             tag in immortalwrt/rootfs is labelled linux/amd64, including the
#             ones whose contents are aarch64, so their metadata cannot be
#             used to select an image. Unpacking the tarball ourselves lets us
#             stamp honest platform metadata.
#
# Built by owlab, which pins every ARG below from owlab.yaml.

ARG BASE_STAGE=upstream
# Both of these must be declared here, before the first FROM: an ARG inside a
# stage is scoped to that stage, and `FROM ${BASE_STAGE}` would then resolve
# against an empty base name.
#
# The default is never what builds an image -- owlab passes BASE_IMAGE on every
# invocation -- but it is read as documentation, so it is held to the pin in
# owlab.yaml by `tools/pins.sh check`. It is also the one place the tarball
# fallback does not reach: `owlab context` asks the registry first and switches
# to ROOTFS_URL when the release has no container image yet, and on 2026-09-04
# `openwrt/rootfs:x86_64-25.12.5` was exactly that -- tagged on the download
# server, not yet mirrored to Docker Hub. A bare `docker build images/` with
# these defaults would have failed on the pull while every owlab build of the
# same release succeeded.
ARG BASE_IMAGE=openwrt/rootfs:x86_64-25.12.5
ARG ROOTFS_URL=
ARG ALPINE_TAG=3.22

# BASE_IMAGE is set to `scratch` on the tarball path, and that is not cosmetic:
# buildkit resolves the metadata for every FROM in this file before it prunes
# the stages the target does not need, so a tarball build would otherwise fail
# on an image it never intends to pull.
FROM ${BASE_IMAGE} AS upstream

# Unreachable, and therefore never even fetched, unless BASE_STAGE=tarball:
# buildkit builds only the stages the target depends on. That is what lets
# ROOTFS_URL default to empty (an `ADD ""` is a hard error) without the
# upstream path ever pulling alpine.
FROM alpine:${ALPINE_TAG} AS unpack
ARG ROOTFS_URL
ADD ${ROOTFS_URL} /tmp/rootfs.tar.gz
RUN mkdir -p /rootfs && tar -xzf /tmp/rootfs.tar.gz -C /rootfs

# COPY rather than letting ADD unpack: `ADD <url>` does not extract a remote
# archive. A faithful copy is only possible because these rootfs tarballs
# carry no device nodes — procd mounts devtmpfs on /dev at boot anyway.
FROM scratch AS tarball
COPY --from=unpack /rootfs/ /
# scratch carries no metadata, so the PATH the RUNs below rely on must be
# stated explicitly.
ENV PATH=/usr/sbin:/usr/bin:/sbin:/bin

FROM ${BASE_STAGE}

# The rootfs has no /var/lock and friends — on a real router procd's boot
# creates them under the /var -> /tmp tmpfs. The package manager below runs
# before any of that, so they have to exist now.
RUN mkdir -p /var/lock /var/run /var/log /var/state

# The feeds are deliberately left EXACTLY as the rootfs shipped them.
#
# It is tempting to strip the kmods line — it is pinned to an exact kernel
# build hash (.../kmods/6.12.87-1-1f1a8723.../) and nothing in it can ever be
# loaded, because the running kernel is the host's and not OpenWrt's. The
# widely-copied container recipes do exactly that. Measured here, it is wrong:
# the kmods feed is what resolves the kmod-* DEPENDENCIES of ordinary
# packages, so without it `apk add luci-app-sqm` fails with
# "kmod-ifb (no such package)". The modules install as files and simply never
# load, which costs a few hundred KB and a complaint from kmodloader at boot —
# far cheaper than a package manager that cannot install half the feed.
#
# Leaving them alone is also what keeps the version pin correct. apk records
# hard pins in /etc/apk/world (base-files=1707~4ccb782af7), so a 25.12.4
# rootfs pointed at the 25.12.5 feed fails every install with
# "breaks: world[...]". The feeds that shipped in the image already name this
# image's exact point release; any rewriting risks breaking that.

# The package set, chosen in owlab.yaml.
#
# Each package is installed on its own and a failure is reported rather than
# fatal. Feeds differ between releases and between OpenWrt and its forks, and
# a package list that is valid on 25.12 but has one name missing on 24.10
# should cost the developer that one package, not the whole image.
ARG PKG_MANAGER=apk
ARG PACKAGES="luci-light"
RUN set -eu; \
	if [ "$PKG_MANAGER" = "apk" ]; then \
		apk update; \
		for p in $PACKAGES; do apk add "$p" 2>/dev/null || echo "owlab: skip (not in this feed): $p"; done; \
	else \
		opkg update; \
		for p in $PACKAGES; do opkg install "$p" 2>/dev/null || echo "owlab: skip (not in this feed): $p"; done; \
	fi

# Packages that do not live in any feed — the developer's own package, or
# anything published on GitHub Releases.
#
# owlab downloads these on the host and stages them per router, because a
# stock rootfs has no curl and its busybox wget cannot do TLS: an in-image
# download would depend on which packages the user happened to choose.
# Per-router, because the two managers do not share a naming scheme and a
# 24.10 box must never be handed a .apk built for 25.12.
#
# These are installed WITHOUT SIGNATURE VERIFICATION. Projects publishing this
# way typically sign with usign and ship a .sig beside the artifact, but
# verifying it needs their public key, which is exactly the trust decision a
# dev container should not be making silently. Treat anything installed here
# as trusted-by-URL only. Do not copy this pattern into anything that ships.
#
# --force-overwrite is needed on opkg and is not a shortcut. These packages
# pull dependencies that replace a file another package already owns —
# luci-app-openclash needs dnsmasq-full, which ships /etc/init.d/dnsmasq and
# collides with the dnsmasq in the stock image:
#
#   check_data_file_clashes: Package dnsmasq-full wants to install file
#   /etc/init.d/dnsmasq. But that file is already provided by package dnsmasq
#
# On a real router that check is a guard worth having. Here the replacement is
# exactly what was asked for, and ImmortalWrt already ships dnsmasq-full, so
# without this the same config produces a different router on OpenWrt 24.10
# than on the other three.
#
# A failure here is FATAL, unlike the feed packages above. The distinction is
# that a feed name can legitimately be missing on an older release, while
# these are named by URL: the developer said "install this file", and a build
# that reports success without it hands them a router that is quietly missing
# the thing they are testing against.
ARG ROUTER_ID=""
COPY extra/ /tmp/owlab-extra/
#
# All of them in ONE command, in the order owlab staged them. These packages
# routinely depend on each other — luci-app-podkop requires podkop — and
# installing one at a time makes that a hard error ("unable to select
# packages") for anything but the luckiest ordering. Handed the whole set, the
# package manager resolves among them.
#
# opkg's index is refreshed HERE, in the layer that uses it, and that is the
# whole reason the line exists. The `opkg update` above runs in a different
# RUN, whose inputs are the release and the feed package list; buildkit keeps
# that layer for as long as those hold, while this one re-runs on every change
# to the staged files. The two are routinely months apart.
#
# That is fatal because a pinned point release is not frozen. OpenWrt rebuilds
# the packages inside `releases/24.10.8/` in place, without bumping a version,
# so an index from an older build names bytes the server no longer serves:
#
#   image built 2026-07-29  bash 5.2.37-r1  Size 473650  SHA256 f1872e60...
#   downloads.openwrt.org   bash 5.2.37-r1  Size 473647  SHA256 20eaa220...
#
#   opkg_install_pkg: Checksum or size mismatch for package bash. Either the
#   opkg or the package index are corrupt. Try 'opkg update'.
#
# It bites here rather than above because a local package is installed by
# path, but its DEPENDENCIES still come out of the index.
#
# apk gets no equivalent line, measured: apk-tools 3.0.5 revalidates a cached
# index older than --cache-max-age (4 hours by default) on its own, so an
# `apk add` inside a month-old image re-downloads every APKINDEX before it
# resolves anything, and a second one seconds later does not. opkg has no such
# policy — it reads whatever the last `opkg update` left and never asks.
#
# Inside the `if`, so a build with no out-of-feed package fetches nothing, and
# in this layer only, so nothing that cached before still caches.
RUN set -eu; \
	dir="/tmp/owlab-extra/$ROUTER_ID"; \
	set -- ; \
	for f in "$dir"/*; do [ -f "$f" ] && set -- "$@" "$f"; done; \
	if [ "$#" -gt 0 ]; then \
		for f in "$@"; do echo "owlab: installing $(basename "$f" | cut -c4-)"; done; \
		if [ "$PKG_MANAGER" = "apk" ]; then \
			apk add --allow-untrusted --force-overwrite "$@"; \
		else \
			opkg update; \
			opkg install --force-overwrite "$@"; \
		fi; \
	fi; \
	rm -rf /tmp/owlab-extra

# The root password, empty by default.
#
# An empty password is what a stock OpenWrt rootfs ships with, and it is the
# right default here: LuCI still shows its login form but accepts an empty
# field, so there is nothing to look up and nothing to remember on a
# throwaway box. ssh key auth is unaffected — the entrypoint installs the
# host's public key.
#
# Set one with OWLAB_ROOT_PASSWORD when the box needs to be reachable by
# password, e.g. for tooling that authenticates for real.
#
# Cleared rather than skipped when empty, because this same Dockerfile also
# runs on top of an already-built owlab image that may carry one.
ARG ROOT_PASSWORD=""
RUN set -eu; \
	if [ -n "$ROOT_PASSWORD" ]; then \
		printf '%s\n%s\n' "$ROOT_PASSWORD" "$ROOT_PASSWORD" | passwd root >/dev/null; \
	else \
		passwd -d root >/dev/null 2>&1 || sed -i 's|^root:[^:]*:|root::|' /etc/shadow; \
	fi

# Host keys are baked into the image rather than generated on first boot: a
# dev container is recreated constantly, and a host key that changes every
# time turns every ssh and scp into a host-key prompt.
#
# These keys are PUBLIC — they are in a published image and shared by every
# user of it. That is the point: they authenticate nothing, they only keep
# ssh from complaining. Never reuse this image where the host key matters.
#
# Generated only when absent, because this same Dockerfile runs on top of an
# already-built owlab image when a router names one in image:. dropbearkey
# refuses to overwrite an existing key and exits non-zero, which would fail
# every prebuilt build at a step that has nothing to do with what changed.
RUN set -eu; \
	mkdir -p /etc/dropbear; \
	[ -f /etc/dropbear/dropbear_ed25519_host_key ] \
		|| dropbearkey -t ed25519 -f /etc/dropbear/dropbear_ed25519_host_key >/dev/null 2>&1; \
	[ -f /etc/dropbear/dropbear_rsa_host_key ] \
		|| dropbearkey -t rsa -s 2048 -f /etc/dropbear/dropbear_rsa_host_key >/dev/null 2>&1

# Take ujail out of procd's reach, so that jailed services actually run.
#
# procd jails a good number of them — dnsmasq, ntpd, others — and a jail needs
# namespaces the container is not allowed to create:
#
#   $ ujail -n test -p -- /bin/true
#   jail: failed to clone/fork: Operation not permitted
#
# procd reports that as a crash and respawns, so the service ends up in a
# permanent crash loop and says so only as "Instance dnsmasq::cfg01411c s in a
# crash loop 6 crashes". Everything downstream then fails for reasons that
# never mention dnsmasq: no DNS server on the router at all, and any package
# that configures dnsmasq to forward somewhere — podkop pointing it at
# sing-box, for one — is configuring a daemon that is not running.
#
# procd falls back to running the service directly when ujail is absent, which
# is why renaming it is the fix rather than a workaround. The alternative is
# granting the container CAP_SYS_ADMIN, which is close enough to root on the
# host that a throwaway dev box has no business asking for it. What is lost is
# a security boundary between services inside a container that is already a
# single trust domain.
#
# This is a container concern only. A fidelity-vm router runs its own kernel,
# where ujail works exactly as it does on hardware.
RUN [ -x /sbin/ujail ] && mv /sbin/ujail /sbin/ujail.disabled-by-owlab || true

COPY rootfs-extra/ /

# Provenance and licence material, baked in at a known path.
#
# Publishing an image built from an OpenWrt rootfs redistributes GPL'd
# binaries. Upstream publishes the manifest, a CycloneDX SBOM with per-package
# licences, and buildinfo files pinning the tree and every feed to an exact
# commit — but none of it INSIDE the rootfs, which carries no licence texts and
# no copyright notices at all. Staging it here is what makes the image
# self-documenting rather than leaving every downstream user to reconstruct it.
COPY compliance/ /tmp/owlab-compliance/
RUN set -eu; \
	src="/tmp/owlab-compliance/$ROUTER_ID"; \
	if [ -d "$src" ]; then \
		mkdir -p /usr/share/owlab/compliance; \
		cp -r "$src"/. /usr/share/owlab/compliance/; \
	fi; \
	rm -rf /tmp/owlab-compliance

# Which fixture profiles this image should apply on first boot. The profiles
# themselves are all present in the image; this file selects them, so the same
# layer cache serves every combination.
ARG FIXTURES=""
# The theme to activate on first boot, if the project is a theme. Installing a
# theme package only registers it; see 95_owlab-base.
ARG THEME=""
RUN mkdir -p /etc/owlab \
	&& printf '%s\n' "$FIXTURES" > /etc/owlab/fixtures \
	&& printf '%s' "$THEME" > /etc/owlab/theme

RUN chmod +x /usr/lib/owlab/* /etc/uci-defaults/* \
	/usr/share/owlab/fixtures/* /etc/rc.local 2>/dev/null || true

EXPOSE 22 80
STOPSIGNAL SIGTERM
# Not /sbin/init directly: the entrypoint has to read the address Docker put
# on eth0 while it is still there, and write the network config that
# config_generate would otherwise invent. Read usr/lib/owlab/entrypoint.sh.
CMD ["/usr/lib/owlab/entrypoint.sh"]
