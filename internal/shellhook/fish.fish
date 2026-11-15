# ticktack shell hook for fish. Load it from config.fish (installed by `ticktack init`).
if command -v ticktack >/dev/null 2>&1
    function _ticktack_hook_prompt --on-event fish_prompt
        if set -q TICKTACK_HOOK_DISABLED
            return 0
        end
        ticktack hook --cwd "$PWD" >/dev/null 2>&1 &
        disown 2>/dev/null
    end
end
