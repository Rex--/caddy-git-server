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
The git_server will serve bare git repositories that are recursively contained
within the root directory. The git_server only responds to git clients
('Git-Protocol' header is present OR a user agent starting with 'git'), unless
the browse page is enabled, in which case a request to the root of each
repository returns a small info page.

You can create a bare repository with the `--bare` flag, no special setup is
required. It is only required that this bare repository be contained in the
`<root>` directory (or subdirectory).

The following will clone a repository on `example.com` that is located at
`<root>/git/example.git`:
```
git clone https://example.com/git/example.git
```


## Reference

**Caddyfile** - The `git_server` directive attempts to mimic the built-in
`file_server` directive +/- a few options.
```
git_server <match> [browse] {
    root <path>
    template <template.html>
    protocol dumb|smart|both
}
```

- `<match>` - request pattern to match
- `browse` - enable repository browser (available at the root of the repo)
- `root <path>` - root path of git directories
- `template <template.html>` - template to use for browse page
- `protocol dumb|smart|both` - git http transfer protocol to use


**JSON**
```
{
    "handler": "git_server",
    "root": "<path>",
    "browse": true|false,
    "template": "<browse.html>",
    "protocol": "dumb|smart|both"
}
```
