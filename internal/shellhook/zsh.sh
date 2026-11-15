# ticktack shell hook for zsh. Load it from ~/.zshrc (installed by `ticktack init`).
if command -v ticktack >/dev/null 2>&1; then
  _ticktack_hook_precmd() {
    [[ -n "${TICKTACK_HOOK_DISABLED-}" ]] && return 0
    ( ticktack hook --cwd "$PWD" >/dev/null 2>&1 & )
  }
  autoload -Uz add-zsh-hook
  add-zsh-hook precmd _ticktack_hook_precmd
fi
