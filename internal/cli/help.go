package cli

// Help is the text of `agtop --help`.
const Help = `Usage: agtop [options]
       agtop setup [--remove] [--yes] [--dry-run] [--config <path>]
       agtop statusline-hook [--print]

Shows Claude Code sessions live.

Options:
  --config <path>           config file to read (default:
                            $XDG_CONFIG_HOME/agtop/config.toml, else
                            ~/.config/agtop/config.toml)
  -h, --help                show this help

Config file (TOML; optional unless given with --config). Only the tables
[profiles], [theme], [glyphs] and [layout] exist; any other key is an error:
  [profiles]
  personal = "~/.claude"
  work = "~/.claude-work"
[profiles] maps a label (shown in the viewer) to a Claude Code config
directory, in the order of the file: an absolute path or starting with '~'
(only expanded at the start). Two labels for the same directory are an error.

Appearance (all optional, independent of [profiles]; the defaults are today's
look). Colors are chalk names (black, red, green, yellow, blue, magenta, cyan,
white, gray and the *Bright forms, e.g. cyanBright) or #rgb / #rrggbb:
  [theme]
  preset = "dark"                  dark | light
  busy = "yellow"                  per role: muted accent busy shell waiting idle
  waiting = "#ff5f5f"              pending done failed ok warn critical link
  profiles = ["magenta", "blue"]   palette of the profiles (cycles)
  [glyphs]
  preset = "ascii"                 unicode | ascii (+-| borders, # . bars)
  dot = "*"                        per slot: barFull barEmpty dot pointer left
  spinner = ["-", "\\", "|", "/"]  up right down middot dash expanded collapsed
                                   ellipsis enter gear cross reset, spinner
  [layout]
  top_bar = true                   false: never show the usage/activity bar
  time = "relative"                relative ("5m ago") | absolute ("14:03:21")
  sparkline = true                 false: profile lines with the usage only
A glyph must take exactly 1 terminal column and have no control characters.
NO_COLOR still turns every color off, whatever the theme.

Watched directories (the first source with a value wins; they don't add up):
  1. [profiles] in the config file    only the listed directories
  2. env CLAUDE_CONFIG_DIR          if set (the same env Claude Code uses)
  3. ~/.claude
  Other profiles are not discovered automatically: with more than one, list
  them under [profiles]. If the default directory (2 or 3) does not exist, the
  viewer says where it looked; list the directory under [profiles].
On the first run without a config file, agtop creates it with the directory it
would watch (3 or 2) and commented hints for ~/.claude and ~/.claude-* directories (also in
non-interactive runs). From then
on the file wins: CLAUDE_CONFIG_DIR no longer applies.

Context window (for the usage %), per model, in this order:
  1. size reported by Claude Code for the session (needs agtop setup)
  2. size reported by another session of the same profile and model
  3. [1m] suffix in the model id → 1M
  4. table by model generation: Opus and Sonnet from 4.6, Fable and Haiku
     from 5 → 1M; earlier generations → 200k
The size is exact except when the model is unknown (200k by default) or when
usage exceeds the table window: then the viewer assumes 1M. Both are marked
with ~ (estimated). Without agtop setup, only 3, 4 and that estimate apply.

Each directory must have projects/; without sessions/ the viewer warns, since
it cannot detect live sessions. With a single profile, the screen shows
neither the profile label nor the filter (p).

Full screen: in a terminal, the viewer takes over the whole screen (the
alternate screen, like vim/htop), with the status bar pinned to the bottom,
and restores the terminal on quit (also on Ctrl-C, signal or error). Without
a terminal on stdin or stdout (pipe, redirection), it warns on stderr,
renders once and exits. In full screen, the process's own messages (console,
Node warnings, stderr) are held, counted in the status bar, and written to
stderr after quitting. Suspending (SIGTSTP) gives the terminal back; on
continue (fg), the screen is redrawn.

Each session shows as a box with its active agents hanging below it.

Plan usage (top bar): one line per profile with the 5-hour and 7-day usage
limits, their reset times and the profile's activity over the last hour.
Claude Code only hands those limits to the status line command, so agtop
records them with a hook placed in front of your status line. To wire it:
  agtop setup               for each watched directory (as in the viewer),
                            shows the statusLine change in its settings.json
                            and asks before writing; your status line command
                            keeps running, now behind the hook (without one,
                            the hook prints a short line: agtop · 5h 6% · 7d 30%)
  agtop setup --remove      puts the original statusLine back and deletes
                            <config dir>/agtop/
  --yes, -y                 apply without asking (needed without a terminal)
  --dry-run                 only show what would change
setup backs settings.json up as settings.json.bak-agtop-<time> before writing
and remembers the original statusLine in <config dir>/agtop/setup.json. A
statusLine in settings.local.json or in the project's .claude/ overrides it
(setup warns). The status line picks up the change on its next render.
statusline-hook copies its stdin to stdout unchanged (with --print, it prints
the short line instead), stores only the usage numbers in
<config dir>/agtop/usage.json (the profile's CLAUDE_CONFIG_DIR, else
~/.claude), the context window size of each session in
<config dir>/agtop/context.json (session id, model id, size) and always exits
0. Data older than 10 minutes, or whose window
has already reset, shows as (stale). On a short terminal the top bar is
hidden before the tree loses room.

Keys:
  arrows / h j k l  move between sessions (in the detail, between agents)
  Enter             open the session (in the detail, expand the agent)
  x                 close the selected session in the tree (SIGTERM, after a confirmation)
  Esc / Backspace   back to the tree (or close the help); in the tree, clear
                    the selection
  Tab               switch the detail panel (agents / pending tools)
  p                 filter by profile (all, then each label); only with 2+
                    profiles
  ?                 show or hide the help (keys, directories, errors)
  Ctrl-L            redraw the whole screen (if something outside garbled it)
  q / Ctrl-C        quit

Environment variables:
  CLAUDE_CONFIG_DIR      default directory (see above)
`
