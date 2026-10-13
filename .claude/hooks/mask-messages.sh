# Sourced by the Bash hooks. mask_messages prints its argument with the quoted value of each
# -m or --message option of a git commit, tag, notes or merge replaced by underscores, so the
# words a commit message names (fix: let helm upgrade keep it) are not taken for commands.
# It follows the shell's quoting: a -m inside quotes is text, not an option, so "-m '" cannot
# open a fake message that would hide what follows. Only a -m of a command that starts with
# git and names one of those subcommands is masked: bash -c -m "git push" is not a message.
# A $( or a backtick inside a double-quoted message runs, so the rest of the line is kept as
# is from there. A command on several lines is returned unmasked: the quoting of a line then
# depends on the lines before it.
mask_messages() {
  case "$1" in
    *'
'* | *"$(printf '\r')"*)
      printf '%s\n' "$1"
      return
      ;;
  esac
  printf '%s\n' "$1" | awk -v sq="'" '
    {
      s = $0; out = ""; st = 0; mask = 0; opt = 0; seg = ""; n = length(s)
      for (i = 1; i <= n; i++) {
        c = substr(s, i, 1)
        if (st == 0) {
          if (c == "\\") { out = out c substr(s, i + 1, 1); seg = seg c substr(s, i + 1, 1); i++; continue }
          if ((c == sq || c == "\"") && opt) { mask = 1 }
          opt = 0
          if (c == sq) { st = 1; out = out c; seg = seg "Q"; continue }
          if (c == "\"") { st = 2; out = out c; seg = seg "Q"; continue }
          if (c ~ /[;&|()`]/) { seg = "" ; out = out c; continue }
          rest = substr(s, i)
          prev = (i == 1) ? " " : substr(s, i - 1, 1)
          if (prev ~ /[ \t]/ && match(rest, /^(-m|--message)(=|[ \t]+)/) &&
              seg ~ /^[ \t]*([A-Za-z_][A-Za-z0-9_]*=[^ \t]*[ \t]+)*git[ \t]+([^ \t]+[ \t]+)*(commit|tag|notes|merge)[ \t]/) {
            out = out substr(rest, 1, RLENGTH); seg = seg substr(rest, 1, RLENGTH); i += RLENGTH - 1; opt = 1; continue
          }
          out = out c; seg = seg c
        } else if (st == 1) {
          if (c == sq) { st = 0; mask = 0; out = out c } else { out = out (mask ? "_" : c) }
        } else {
          if (c == "\\") { out = out (mask ? "__" : c substr(s, i + 1, 1)); i++; continue }
          if (c == "\"") { st = 0; mask = 0; out = out c; continue }
          if (mask && (c == "`" || (c == "$" && substr(s, i + 1, 1) == "("))) { out = out substr(s, i); break }
          out = out (mask ? "_" : c)
        }
      }
      print out
    }'
}
