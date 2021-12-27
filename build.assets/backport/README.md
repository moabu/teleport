# Automatic Backport Bot

An automatic backport bot is a tool to use for creating backports.  


## Prerequisites
- Install the [Github CLI](https://github.com/cli/cli) and [authenticate](https://cli.github.com/manual/gh_auth_login). 
- Install Git. 
  
## Use

This tool is intended to be used when then changes on a branch are approved to be merged into the `master` branch. The developer should **only** create backports on branches they have created unless otherwise asked. 

### Flags 

| Name     | Description | Required | 
| ----------- | ----------- |---|
| from     | Branch with changes to backport.|Yes|
| to   | List of comma-separated branches to backport to.|Yes|

Running the bot:
``` bash
  go run main.go --from my-branch --to branch/v7,branch/v8
```

From the project root with `make`:
```
  make backport from=test-new to=branch/v7,branch/v8
```

