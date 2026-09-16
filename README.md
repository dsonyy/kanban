<div align="center">

# kk

**workflow for coding agents**

</div>

> **_Agent:_** _Completed all tasks according to the plan. Proposed new product directions in the handoff. Tests passed. PRs created. Proceed with the next product iteration?_  
> **_You:_** _kk_

## Getting started

You need Go, Node.js, tmux, git and [just](https://just.systems).

```sh
just build
./kk
```

`kk` prints a web address with an access token. Open it, point kk at a project folder and add tasks.

For development, `just dev` runs the server with hot reload and opens the browser.
