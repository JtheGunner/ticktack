# ticktack shell hook for bash. Load it from ~/.bashrc (installed by `ticktack init`).
if command -v ticktack >/dev/null 2>&1; then
  _ticktack_hook_prompt() {
    # The hook runs first in PROMPT_COMMAND; hand the status of the last
    # command on to the entries behind it.
    local ticktack_status=$?
    if [[ -z "${TICKTACK_HOOK_DISABLED-}" ]]; then
      ( ticktack hook --cwd "$PWD" >/dev/null 2>&1 & )
    fi
    return "$ticktack_status"
  }
  case ";${PROMPT_COMMAND-};" in
    *";_ticktack_hook_prompt;"*) ;;
    *) PROMPT_COMMAND="_ticktack_hook_prompt${PROMPT_COMMAND:+;$PROMPT_COMMAND}" ;;
  esac
fi
