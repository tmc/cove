# Recover From A Partial Homebrew Install

If the Homebrew installer was interrupted before `/opt/homebrew/bin/brew` was
created, remove the partial tree before retrying:

```sh
sudo rm -rf /opt/homebrew/{Cellar,Library,bin,etc,opt,sbin,share,var}
sudo rmdir /opt/homebrew
```

`rmdir` only removes `/opt/homebrew` when it is empty. If it fails, inspect the
remaining files before deleting them.

After cleanup, either install CLT explicitly:

```sh
cove vzscript run xcode-cli homebrew
```

or opt in to Homebrew's upstream CLT install:

```sh
cove vzscript run -env COVE_HOMEBREW_ACCEPT_CLT=1 homebrew
```
