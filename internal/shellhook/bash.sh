# ticktack shell hook for bash. Load it from ~/.bashrc (installed by `ticktack init`).
if command -v ticktack >/dev/null 2>&1; then
  _ticktack_hook_prompt() {
    [[ -n "${TICKTACK_HOOK_DISABLED-}" ]] && return 0
    ( ticktack hook --cwd "$PWD" >/dev/null 2>&1 & )
  }
  case ";${PROMPT_COMMAND-};" in
    *";_ticktack_hook_prompt;"*) ;;
    *) PROMPT_COMMAND="_ticktack_hook_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
  esac
fi
