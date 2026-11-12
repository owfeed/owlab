# Examples

[Русская версия](README_ru.md)

Configs that do something real. Point owlab at one from anywhere:

```console
$ owlab --config examples/luci-theme-footstrap up
```

| | |
|---|---|
| [luci-theme-footstrap](luci-theme-footstrap/owlab.yaml) | A LuCI theme: four routers to cover both package managers and both LuCI feeds, a build step for the generated CSS, and out-of-feed apps whose stylesheets it has to share a page with |

- The projects develop against these configs as they are.
- They are written for editing the project's own source.
- To work against a released version, drop the `install:`/`post_sync:` block
  and name the package under `extra_packages:`.
