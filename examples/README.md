# Examples

[Русская версия](README_ru.md)

Configs that do something real. Point owlab at one from anywhere:

```console
$ owlab --config examples/luci-theme-footstrap up
```

| | |
|---|---|
| [luci-theme-footstrap](luci-theme-footstrap/owlab.yaml) | A LuCI theme: four routers to cover both package managers and both LuCI feeds, a build step for the generated CSS, and third-party apps whose stylesheets it has to share a page with |

These are the configs the projects actually develop against, not sketches.
They are written for editing the project's own source; to work against a
released version instead, drop the `install:`/`post_sync:` block and name the
package under `extra_packages:`.
