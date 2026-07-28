# Examples

[Русская версия](README_ru.md)

Configs that do something real. Point owlab at one from anywhere:

```console
$ owlab --config examples/podkop up
```

| | |
|---|---|
| [luci-theme-footstrap](luci-theme-footstrap/owlab.yaml) | A LuCI theme: four routers to cover both package managers and both LuCI feeds, a build step for the generated CSS, and third-party apps whose stylesheets it has to share a page with |
| [podkop](podkop/owlab.yaml) | A package that rewrites dnsmasq, builds a sing-box config and installs nftables rules — including a `fidelity: vm` router, because tproxy needs a kernel module that a container cannot load |

Both are the configs those projects actually develop against, not sketches.
They are written for editing the project's own source; to work against a
released version instead, drop the `install:`/`post_sync:` block and name the
package under `extra_packages:`.
