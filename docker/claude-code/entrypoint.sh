#!/bin/bash
set -e

if [ -f "$HOME/.gitconfig-host" ]; then
    cp "$HOME/.gitconfig-host" "$HOME/.gitconfig"
    git config --global commit.gpgsign false
    git config --global core.excludesfile "$HOME/.gitignore_global-host"
fi

exec "$@"
