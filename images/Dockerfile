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
ARG BASE_IMAGE=openwrt/rootfs:x86_64-25.12.4
ARG ROOTFS_URL=
ARG ALPINE_TAG=3.22

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

# LuCI accepts an empty root password, but the tooling authenticates for real
# — ssh's own PasswordAuthentication, and whatever the developer's browser
# does — so there has to be one.
ARG ROOT_PASSWORD=owlab
RUN printf '%s\n%s\n' "$ROOT_PASSWORD" "$ROOT_PASSWORD" | passwd root >/dev/null

# Host keys are baked into the image rather than generated on first boot: a
# dev container is recreated constantly, and a host key that changes every
# time turns every ssh and scp into a host-key prompt.
#
# These keys are PUBLIC — they are in a published image and shared by every
# user of it. That is the point: they authenticate nothing, they only keep
# ssh from complaining. Never reuse this image where the host key matters.
RUN dropbearkey -t ed25519 -f /etc/dropbear/dropbear_ed25519_host_key >/dev/null 2>&1 \
	&& dropbearkey -t rsa -s 2048 -f /etc/dropbear/dropbear_rsa_host_key >/dev/null 2>&1

COPY rootfs-extra/ /

# Which fixture profiles this image should apply on first boot. The profiles
# themselves are all present in the image; this file selects them, so the same
# layer cache serves every combination.
ARG FIXTURES=""
RUN mkdir -p /etc/owlab \
	&& printf '%s\n' "$FIXTURES" > /etc/owlab/fixtures

RUN chmod +x /usr/lib/owlab/* /etc/uci-defaults/* \
	/usr/share/owlab/fixtures/* /etc/rc.local 2>/dev/null || true

EXPOSE 22 80
STOPSIGNAL SIGTERM
# Not /sbin/init directly: the entrypoint has to read the address Docker put
# on eth0 while it is still there, and write the network config that
# config_generate would otherwise invent. Read usr/lib/owlab/entrypoint.sh.
CMD ["/usr/lib/owlab/entrypoint.sh"]
