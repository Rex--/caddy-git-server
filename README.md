# caddy-git-server

Provides a `git_server` caddy module for serving git repositories.

This module implements the necessary git http transfer protocols to serve
clone-able git repositories. This allows you to self host your code with a
simple caddy server + config.


## Installation
You must build a custom version of caddy to use this module. Luckily, the
[`xcaddy`](https://github.com/caddyserver/xcaddy) command makes this easy:

```
xcaddy build --with github.com/Rex--/caddy-git-server
```


## Configuration
Since `git_server` is an http.handler module, you must manually define the
order in which it is serviced relative to the other handlers caddy provides.
The Caddyfile provides two ways to do this:

1. Route Block - You can manually define the routing order by putting the
handlers into a `route` block. Even if you plan to use only the `git_server`
handler, you still have to wrap it in a `route` block.
```
:8080 {
    route {
        <handler(s)>
    }
}
```

2. Global `order` directive - At the top of the Caddyfile you can define some
global options that apply to the whole config. It is recommended that you order
the `git_server` directive `before file_server`. Another option is ordering it
`last`, this is useful for setups that only have the git_server.
```
{
    order git_server before file_server
        OR
    order git_server last
}
```
### **NOTE:** Examples assume you have ordered the directive globally.

<br>

The simplest of configurations takes no arguments if you have defined a `root`:
```
git.example.com {
	root * /srv/git/
    git_server
}
```

## Usage

Caddy-git-server will serve bare git repositories that are recursively contained
within the root directory. Caddy-git-server only responds to git clients
('Git-Protocol' header is present OR a user agent starting with 'git'), unless
the browse page is enabled, in which case a request to the root of each
repository returns a page enabling you to browse source code, view generated
markdown and kicad files, and access git logs and diffs.

### Creating Repositories

You can create a bare repository with the `--bare` flag, no special setup is
required. It is only required that this bare repository be contained in the
`<root>` directory (or subdirectory).

### Pulling

The following will clone a repository on `example.com` that is located at
`<root>/git/example.git`:
```
git clone https://example.com/git/example.git
```

The proper clone url for each repository is also displayed on it's browse page.

### Pushing

Caddy-git-server does not support pushing and there are no plans to implement it.

It is recommened to set up ssh access with permissions to access the `<root>`
directory. You can then push to the reposity over ssh using something like:
```
git add remote <remote> <remote_ip>:/path/to/<root>/example.git
git push <remote> <branch>
```


## Reference

**Caddyfile** - The `git_server` directive attempts to mimic the built-in
`file_server` directive +/- a few options.
```
git_server [browse] {
    root <path>
    template_dir <path/to/templates/>
}
```

<!-- - `<match>` - request pattern to match -->
- `browse` - enable repository browser (available at the root of the repo)
- `root <path>` - root path of git directories
- `depth <int>` - fow far to recurse into root directory to find repositories. 0 = no limit, 1 = only root, etc.
- `template_dir <path>` - directory containing templates that override the embedded defaults
- `asset_dir <path>` - directory containing assets that oveerride the embedded defaults


**JSON**
```
{
    "handler": "git_server",
    "root": "<path>",
    "browse": true|false,
    "template_dir": "<path>"
}
```
