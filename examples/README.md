# Examples

Configs that do something real. Point owlab at one from anywhere:

```console
$ owlab --config examples/podkop up
```

| | |
|---|---|
| [podkop](podkop/owlab.yaml) | Developing a package that rewrites dnsmasq, builds a sing-box config and installs nftables rules |

The example above is written for editing that project's own source. To use a
released version instead, drop the `install:`/`post_sync:` block and name the
package under `extra_packages:`.
