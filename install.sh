#!/bin/sh
set -eu

repository=rodrigogaraujo/lanhop
releases_url=${LANHOP_RELEASES_URL:-https://github.com/$repository/releases}
guides_url=https://github.com/$repository/blob/main/docs
install_command="curl -fsSL https://raw.githubusercontent.com/$repository/main/install.sh | sh"
unit_name=lanhop.service
agent_label=com.github.rodrigogaraujo.lanhop
app_identifier=com.github.rodrigogaraujo.lanhop
system_root=${LANHOP_SYSTEM_ROOT:-}
udev_rule_path=$system_root/etc/udev/rules.d/60-lanhop.rules
udev_rule='KERNEL=="uinput", SUBSYSTEM=="misc", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"'
uinput_at_boot_path=$system_root/etc/modules-load.d/lanhop.conf
accessibility_pane='x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility'
local_network_pane='x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork'
firewall_tool=$system_root/usr/libexec/ApplicationFirewall/socketfilterfw
check_limit=600
central_wait=60
blocked_patience=15
stopped_patience=25
start_wait=10
service_started=no
showing=no
someone_answers=no

usage() {
  cat <<EOF
Installs lanhop for the current user. Nothing is installed as root.

  $install_command
  $install_command -s -- --uninstall

Options:
  --uninstall   stop lanhop and remove its service, its binary and, on macOS,
                lanhop.app. The key of this machine and its trusted machines are kept
  --help        show this text

Environment:
  LANHOP_VERSION=v0.1.0       install that release instead of the latest one
  LANHOP_INSTALL_DIR=DIR      put the binary in DIR instead of ~/.local/bin. On macOS,
                              lanhop.app goes to /Applications, or to ~/Applications
                              when /Applications cannot be written, and DIR gets the
                              lanhop command
  LANHOP_ROLE=share|receive   share when the keyboard and mouse are plugged into this
                              machine, receive when another machine controls this one
  LANHOP_YES=1                agree to every step and ask nothing

To uninstall from another directory than ~/.local/bin, give LANHOP_INSTALL_DIR again.
EOF
}

say() {
  printf '%s\n' "$*"
}

problem() {
  printf 'lanhop installer: %s\n' "$*" >&2
}

fail() {
  problem "$@"
  exit 1
}

only_described() {
  if [ "$showing" = yes ]; then
    say "    $*"
    return 0
  fi
  if [ "${LANHOP_PRINT_COMMANDS:-}" = 1 ]; then
    say "would run: $*"
    return 0
  fi
  return 1
}

system() {
  only_described "$@" || "$@"
}

write_as_root() {
  only_described "printf '%s\\n' '$2' | sudo tee $1" || printf '%s\n' "$2" | sudo tee "$1" >/dev/null
}

shown() {
  (
    showing=yes
    "$@"
  )
}

open_terminal() {
  if [ "${LANHOP_YES:-}" = 1 ]; then
    return 0
  fi
  terminal=${LANHOP_TERMINAL:-/dev/tty}
  if (exec 3<"$terminal") 2>/dev/null; then
    exec 3<"$terminal"
    someone_answers=yes
  fi
}

ask() {
  reply=
  read -r reply <&3 || true
}

is_yes() {
  case $1 in
    y | Y | yes | Yes | YES) return 0 ;;
  esac
  return 1
}

is_no() {
  case $1 in
    n | N | no | No | NO) return 0 ;;
  esac
  return 1
}

agrees() {
  if [ "${LANHOP_YES:-}" = 1 ]; then
    return 0
  fi
  if [ "$someone_answers" != yes ]; then
    return 1
  fi
  printf '%s [y/N] ' "$1"
  ask
  is_yes "$reply"
}

lanhop_as_the_service_runs_it() {
  (
    unset LANHOP_CONFIG_DIR
    "$binary" "$@"
  )
}

outliving_ctrl_c() {
  trap : INT
  ended=0
  "$@" || ended=$?
  trap - INT
  return "$ended"
}

find_system() {
  system_name=${LANHOP_SYSTEM:-$(uname -s)}
  machine_name=${LANHOP_MACHINE:-$(uname -m)}
  case $system_name in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "lanhop has no build for $system_name. It runs on Linux and on macOS." ;;
  esac
  case $machine_name in
    x86_64 | amd64) arch=amd64 ;;
    aarch64 | arm64) arch=arm64 ;;
    *) fail "lanhop has no build for $machine_name. It is built for x86_64 and for arm64." ;;
  esac
}

settle_paths() {
  install_directory=${LANHOP_INSTALL_DIR:-$HOME/.local/bin}
  case $install_directory in
    /*) ;;
    *) fail "LANHOP_INSTALL_DIR must be an absolute path, and it is $install_directory" ;;
  esac
  binary=$install_directory/lanhop
  command_link=$binary
  earlier_program=
  app=
  if [ "$os" = darwin ]; then
    if is_the_app "$system_root/Applications/lanhop.app" && [ -w "$system_root/Applications" ]; then
      app_directory=$system_root/Applications
    elif is_the_app "$HOME/Applications/lanhop.app" && [ -w "$HOME/Applications" ]; then
      app_directory=$HOME/Applications
    elif [ -w "$system_root/Applications" ]; then
      app_directory=$system_root/Applications
    else
      app_directory=$HOME/Applications
    fi
    config_directory="$HOME/Library/Application Support/lanhop"
    agent_path=$HOME/Library/LaunchAgents/$agent_label.plist
    log_path=$HOME/Library/Logs/lanhop.log
  else
    config_directory=${XDG_CONFIG_HOME:-$HOME/.config}/lanhop
    unit_path=${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/$unit_name
    rule_record=${XDG_STATE_HOME:-$HOME/.local/state}/lanhop/udev-rule-installed
  fi
}

check_role() {
  case ${LANHOP_ROLE:-} in
    '' | share | receive) ;;
    *) fail "LANHOP_ROLE must be share or receive, and it is $LANHOP_ROLE" ;;
  esac
}

download() {
  curl -fsSL "$1" -o "$2" || fail "could not download $1. Check the connection, and that the release exists: https://github.com/$repository/releases"
}

sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{ print $1 }'
  else
    shasum -a 256 "$1" | awk '{ print $1 }'
  fi
}

install_binary() {
  for tool in curl tar awk; do
    command -v "$tool" >/dev/null 2>&1 || fail "$tool is needed to install lanhop, and this machine does not have it"
  done
  command -v sha256sum >/dev/null 2>&1 || command -v shasum >/dev/null 2>&1 || fail "sha256sum or shasum is needed to check the download, and this machine has neither"

  package=lanhop-$os-$arch
  if [ -n "${LANHOP_VERSION:-}" ]; then
    release_url=$releases_url/download/$LANHOP_VERSION
  else
    release_url=$releases_url/latest/download
  fi
  work=$(mktemp -d)
  trap 'rm -rf "$work"' EXIT

  say "Downloading $release_url/$package.tar.gz"
  download "$release_url/$package.tar.gz" "$work/$package.tar.gz"
  download "$release_url/SHA256SUMS" "$work/SHA256SUMS"
  expected=$(awk -v archive="$package.tar.gz" '$2 == archive || $2 == "*" archive { print $1 }' "$work/SHA256SUMS")
  if [ -z "$expected" ]; then
    fail "SHA256SUMS of that release does not list $package.tar.gz, so the download cannot be checked. Nothing was installed."
  fi
  found=$(sha256_of "$work/$package.tar.gz")
  if [ "$found" != "$expected" ]; then
    fail "the download does not match its checksum (SHA256SUMS says $expected, the file has $found). Nothing was installed."
  fi

  tar -xzf "$work/$package.tar.gz" -C "$work"
  if [ "$os" = darwin ] && [ -d "$work/$package/lanhop.app" ]; then
    install_app
  elif [ -f "$work/$package/lanhop" ]; then
    mkdir -p "$install_directory"
    cp "$work/$package/lanhop" "$binary.new"
    chmod 755 "$binary.new"
    mv -f "$binary.new" "$binary"
  else
    fail "$package.tar.gz has no $package/lanhop in it. Nothing was installed."
  fi
  rm -rf "$work"
  trap - EXIT

  installed=$("$binary" version) || fail "$binary was installed, but it does not run on this machine"
  if [ -n "$app" ]; then
    say "Installed $installed in $app"
    say "The lanhop command in $command_link runs it."
  else
    say "Installed $installed in $binary"
  fi
}

install_app() {
  unpacked=$work/$package/lanhop.app
  if [ ! -f "$unpacked/Contents/MacOS/lanhop" ]; then
    fail "$package.tar.gz has no $package/lanhop.app/Contents/MacOS/lanhop in it. Nothing was installed."
  fi
  app=$app_directory/lanhop.app
  binary=$app/Contents/MacOS/lanhop
  if [ -e "$command_link" ] || [ -L "$command_link" ]; then
    case $(readlink "$command_link" 2>/dev/null || true) in
      */lanhop.app/Contents/MacOS/lanhop) ;;
      *) earlier_program=$command_link ;;
    esac
  fi
  mkdir -p "$app_directory" "$install_directory"
  rm -rf "$app".new.* "$app".old.* 2>/dev/null || true
  staged=$app.new.$$
  cp -R "$unpacked" "$staged"
  chmod 755 "$staged/Contents/MacOS/lanhop"
  if [ -e "$app" ]; then
    mv "$app" "$app.old.$$"
  fi
  mv "$staged" "$app"
  rm -rf "$app.old.$$" || problem "could not remove the earlier copy of the app, $app.old.$$"
  rm -f "$command_link.new"
  ln -s "$binary" "$command_link.new"
  mv -f "$command_link.new" "$command_link"
}

advise_about_path() {
  case ":$PATH:" in
    *":$install_directory:"*) ;;
    *)
      cat <<EOF

$install_directory is not on your PATH. To run lanhop by its name, add this line
to the file your shell reads when it starts, such as ~/.profile or ~/.zshrc:

    export PATH="$install_directory:\$PATH"

Until then, type the whole path: $command_link
EOF
      ;;
  esac
}

choose_role() {
  role=${LANHOP_ROLE:-}
  if [ -n "$role" ] || [ "$someone_answers" != yes ]; then
    return 0
  fi
  say ""
  say "One machine has the keyboard and mouse and shares them. The others are controlled by it."
  printf 'Are the keyboard and mouse plugged into this machine? [y/n] '
  ask
  if is_yes "$reply"; then
    role=share
  elif is_no "$reply"; then
    role=receive
  fi
}

explain_what_is_left() {
  cat <<EOF

lanhop is installed, and nothing else was changed on this machine.

To set it up to start by itself, run the installer from a terminal and answer its
questions, or set LANHOP_ROLE=share on the machine with the keyboard and mouse and
LANHOP_ROLE=receive on the others, with LANHOP_YES=1 to agree to every step.

By hand: "lanhop pair" on the machine with the keyboard and "lanhop pair ADDRESS" on
the other, then "lanhop share" on the first and "lanhop receive" on the second.
EOF
}

fits_in_a_service_file() {
  case $1 in
    *\"* | *\\* | *\$* | *%* | *\<* | *\>* | *\&*) return 1 ;;
  esac
  return 0
}

explain_no_service() {
  if [ -n "$app" ]; then
    problem "lanhop cannot be started at login from $binary, because a service file cannot hold a path with \" \\ \$ % < > or &."
    return 0
  fi
  problem "lanhop cannot be started at login from $binary, because a service file cannot hold a path with \" \\ \$ % < > or &. Install it in another directory with LANHOP_INSTALL_DIR."
}

lists_input() {
  tr ' ' '\n' | grep -qx input
}

join_input_group() {
  system sudo usermod -aG input "$user"
}

offer_input_group() {
  if id -Gn "$user" | lists_input; then
    return 0
  fi
  cat <<EOF

lanhop talks to the input layer of the kernel, and only members of the "input" group
may do that. A member of that group can read every key typed on this machine, in any
program, so add only the user who runs lanhop. This adds $user:

EOF
  shown join_input_group
  say ""
  if ! agrees "Run it now?"; then
    say "Not run. \"lanhop $role\" works only as root until $user is in that group."
  elif join_input_group; then
    say "Done. It applies to new sessions: log out and log in again."
  else
    problem "that did not work. $guides_url/setup-linux.md has the step."
  fi
}

write_udev_rule() {
  write_as_root "$udev_rule_path" "$udev_rule"
}

load_uinput() {
  write_as_root "$uinput_at_boot_path" uinput &&
    system sudo modprobe uinput &&
    system sudo udevadm control --reload-rules &&
    system sudo udevadm trigger
}

offer_uinput_rule() {
  if [ -e "$udev_rule_path" ]; then
    return 0
  fi
  cat <<EOF

To be controlled, this machine needs a virtual keyboard and mouse, which lanhop creates
through /dev/uinput. These commands let the "input" group use it, now and after every start:

EOF
  shown write_udev_rule
  shown load_uinput
  say ""
  if ! agrees "Run them now?"; then
    say "Not run. \"lanhop receive\" works only as root until that rule exists."
  elif write_udev_rule; then
    mkdir -p "$(dirname "$rule_record")"
    say "$udev_rule_path" >"$rule_record"
    load_uinput || problem "the rule is written, but it could not be applied now. It applies after a restart."
  else
    problem "that did not work. $guides_url/setup-linux.md has the step."
  fi
}

write_unit() {
  mkdir -p "$(dirname "$unit_path")"
  cat >"$unit_path" <<EOF
[Unit]
Description=lanhop $role
Documentation=https://github.com/$repository

[Service]
ExecStart="$binary" $role
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
EOF
}

enable_unit() {
  system systemctl --user daemon-reload &&
    system systemctl --user enable "$unit_name" &&
    if [ "$input_group_applies" = yes ]; then system systemctl --user restart "$unit_name"; fi
}

offer_to_enable_unit() {
  if id -Gn | lists_input; then
    input_group_applies=yes
  else
    input_group_applies=no
  fi
  cat <<EOF

Wrote the systemd user unit $unit_path
It runs "lanhop $role", and it is off until these commands enable it:

EOF
  shown enable_unit
  say ""
  if ! agrees "Run them now, so that lanhop starts every time you log in?"; then
    say "Not run. Start lanhop by hand with \"lanhop $role\"."
  elif ! enable_unit; then
    problem "systemd did not take it. On a system without systemd, start \"lanhop $role\" with your session in another way."
  elif [ "$input_group_applies" = yes ]; then
    service_started=yes
    say "lanhop is running. See what it does with: journalctl --user -u $unit_name -f"
  else
    say "lanhop starts the next time you log in, when the input group applies to your session."
  fi
}

set_up_linux() {
  user=$(id -un)
  offer_input_group
  if [ "$role" = receive ]; then
    offer_uinput_rule
  fi
  if ! fits_in_a_service_file "$binary"; then
    explain_no_service
    return 0
  fi
  write_unit
  offer_to_enable_unit
}

write_agent() {
  mkdir -p "$(dirname "$agent_path")" "$(dirname "$log_path")"
  associated=
  if [ -n "$app" ]; then
    associated="  <key>AssociatedBundleIdentifiers</key>
  <array>
    <string>$app_identifier</string>
  </array>
"
  fi
  cat >"$agent_path" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>$agent_label</string>
${associated}  <key>ProgramArguments</key>
  <array>
    <string>$binary</string>
    <string>$role</string>
  </array>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <dict>
    <key>SuccessfulExit</key>
    <false/>
  </dict>
  <key>ProcessType</key>
  <string>Interactive</string>
  <key>StandardOutPath</key>
  <string>$log_path</string>
  <key>StandardErrorPath</key>
  <string>$log_path</string>
</dict>
</plist>
EOF
}

explain_accessibility() {
  if [ -n "$app" ]; then
    explain_accessibility_of_the_app
    return 0
  fi
  cat <<EOF

macOS lets a program read and control the keyboard and mouse only after you allow it
under Accessibility. Started at login, the program to allow is lanhop itself, not the
terminal. In System Settings > Privacy & Security > Accessibility:

  1. Click the + button under the list. macOS asks for your password or Touch ID.
  2. Press Command+Shift+G, type $binary
     and press Return, then click Open.
  3. Make sure the switch next to lanhop is on.

If lanhop is already in the list from an earlier install, select it, remove it with the
- button and add it again: macOS ties the permission to the exact program it approved.
$guides_url/setup-macos.md says more.

EOF
}

explain_accessibility_of_the_app() {
  cat <<EOF

macOS lets a program read and control the keyboard and mouse only after you allow it
under Accessibility. Started at login, the program to allow is the lanhop app, not the
terminal. In System Settings > Privacy & Security > Accessibility:

  1. Click the + button under the list. macOS asks for your password or Touch ID.
  2. $(how_to_choose_the_app)
  3. Make sure the switch next to lanhop is on.

If lanhop is already in the list from an earlier install, select it, remove it with the
- button and add it again: macOS ties the permission to the exact program it approved.
$guides_url/setup-macos.md says more.

EOF
}

how_to_choose_the_app() {
  if [ "$app_directory" = "$system_root/Applications" ]; then
    printf 'Choose lanhop in %s and click Open.' "$app_directory"
  else
    printf 'Press Command+Shift+G, type %s and press Return,\n     then choose lanhop and click Open.' "$app_directory"
  fi
}

explain_earlier_program() {
  if [ -z "$earlier_program" ]; then
    return 0
  fi
  cat <<EOF

$earlier_program now runs the lanhop app. For macOS, the lanhop of an earlier install
was another program. If System Settings > Privacy & Security lists a "lanhop" or an
"a.out" from before under Accessibility or Local Network, select it and remove it with
the - button. Switching it on does nothing for the app.
EOF
}

stop_agent() {
  system launchctl bootout "gui/$user_id/$agent_label"
}

start_agent() {
  system launchctl bootstrap "gui/$user_id" "$agent_path"
}

set_up_macos() {
  user_id=$(id -u)
  if ! fits_in_a_service_file "$binary$log_path"; then
    explain_no_service
    return 0
  fi
  cat <<EOF

A launch agent can start "lanhop $role" every time you log in. Its file would be
$agent_path
EOF
  if ! agrees "Install it and start lanhop?"; then
    if [ -e "$agent_path" ]; then
      say "Nothing was changed. The launch agent of an earlier install is still there, and it still starts lanhop when you log in."
    else
      say "Not installed. Start lanhop by hand with \"lanhop $role\", after allowing your terminal as $guides_url/setup-macos.md explains."
    fi
    return 0
  fi
  write_agent
  say "Wrote $agent_path"
  if ! knows_its_status; then
    explain_accessibility
    open_pane "$accessibility_pane"
    if [ "$someone_answers" = yes ]; then
      printf 'Press Enter here when the switch next to lanhop is on. '
      ask
    fi
  fi
  stop_agent 2>/dev/null || true
  if start_agent; then
    service_started=yes
    say "lanhop is running, and it starts every time you log in. It writes what it does in $log_path"
  else
    problem "launchd did not start it now. It starts the next time you log in."
  fi
  offer_firewall
}

open_pane() {
  system open "$1" || problem "System Settings did not open. Open that pane by hand."
}

firewall_is_on() {
  [ -x "$firewall_tool" ] && "$firewall_tool" --getglobalstate 2>/dev/null | grep -q -e enabled -e 'State = [12]'
}

firewall_blocks_everything() {
  "$firewall_tool" --getblockall 2>/dev/null | grep -q -i enabled
}

let_the_app_through() {
  system sudo "$firewall_tool" --add "$app" &&
    system sudo "$firewall_tool" --unblockapp "$app"
}

offer_firewall() {
  if [ "$role" != share ] || [ -z "$app" ] || ! firewall_is_on; then
    return 0
  fi
  if firewall_blocks_everything; then
    cat <<EOF

The firewall of this Mac blocks every incoming connection, so the other machines
cannot connect to lanhop here. In System Settings > Network > Firewall > Options,
switch off "Block all incoming connections".
EOF
    return 0
  fi
  cat <<EOF

The firewall of this Mac is on. The other machines connect to lanhop here, so the
firewall must let the lanhop app accept connections. These commands allow it:

EOF
  shown let_the_app_through
  say ""
  if ! agrees "Run them now?"; then
    say "Not run. The other machines cannot connect until the firewall lets lanhop in: System Settings > Network > Firewall > Options."
  elif ! let_the_app_through; then
    problem "that did not work. Allow lanhop in System Settings > Network > Firewall > Options."
  fi
}

explain_pairing() {
  if [ "$role" = share ]; then
    cat <<EOF

Two machines must be paired once before one controls the other:

    on this machine:        lanhop pair
    on the other machine:   lanhop pair ADDRESS-OF-THIS-MACHINE

Each shows two codes. Check that they match, swapped, and type yes on both.
EOF
    if [ "$someone_answers" != yes ]; then
      return 0
    fi
    say ""
    if agrees "Run \"lanhop pair\" now? It waits for the other machine, and Ctrl+C stops it."; then
      outliving_ctrl_c lanhop_as_the_service_runs_it pair <&3 || problem "the machines are not paired. Run \"lanhop pair\" again when both are ready."
    fi
    return 0
  fi
  cat <<EOF

Two machines must be paired once before one controls the other:

    on the machine with the keyboard:   lanhop pair
    on this machine:                    lanhop pair ADDRESS-OF-THAT-MACHINE

Each shows two codes. Check that they match, swapped, and type yes on both.
EOF
  if [ "$someone_answers" != yes ]; then
    return 0
  fi
  say ""
  say "To pair now, start \"lanhop pair\" on the machine with the keyboard first."
  printf 'Then type its address here, or press Enter to pair later: '
  ask
  if [ -n "$reply" ]; then
    outliving_ctrl_c lanhop_as_the_service_runs_it pair "$reply" <&3 || problem "the machines are not paired. Run \"lanhop pair $reply\" again when both are ready."
  fi
}

knows_its_status() {
  grep -q 'lanhop status' "$binary" 2>/dev/null
}

current_status() {
  line=$(lanhop_as_the_service_runs_it status 2>/dev/null || true)
  if [ -z "$line" ]; then
    line='silent: lanhop status said nothing. Run "lanhop status" to see why.'
  fi
  printf '%s\n' "$line"
}

seconds_now() {
  date +%s
}

tell_once() {
  case " $told " in
    *" $1 "*) return 0 ;;
  esac
  told="$told $1"
  say ""
  say "$2"
}

tell_once_and_wait() {
  case " $told " in
    *" $1 "*) return 0 ;;
  esac
  tell_once "$1" "$2"
  say "Waiting until lanhop connects."
}

ask_for_accessibility() {
  case " $told " in
    *" accessibility "*) return 0 ;;
  esac
  told="$told accessibility"
  explain_accessibility
  open_pane "$accessibility_pane"
  say "Waiting until lanhop has the permission."
}

warn_about_the_local_network_alert() {
  if [ "$os" != darwin ] || [ "$role" != receive ]; then
    return 0
  fi
  tell_once alert "macOS may now ask whether lanhop may find devices on your local network. Click Allow."
}

ask_for_local_network() {
  case " $told " in
    *" local-network "*) return 0 ;;
  esac
  told="$told local-network"
  cat <<EOF

macOS keeps lanhop off the local network, so lanhop cannot reach the other machine.
In System Settings > Privacy & Security > Local Network, switch on lanhop.
An "a.out" in that list is the lanhop of an earlier install, and switching it does
nothing for the app.

EOF
  open_pane "$local_network_pane"
  say "Waiting until lanhop connects."
}

report_status_once() {
  begun=$(seconds_now)
  line=$(current_status)
  while [ "${line%%:*}" = stopped ] && [ $(($(seconds_now) - begun)) -lt "$start_wait" ]; do
    sleep 1
    line=$(current_status)
  done
  if [ "${line%%:*}" = accessibility ]; then
    explain_accessibility
  fi
  say "\"lanhop status\" says at any time what lanhop is doing. Now:"
  say "$line"
}

check_service() {
  if [ "$service_started" != yes ] || ! knows_its_status; then
    return 0
  fi
  say ""
  if [ "$someone_answers" != yes ]; then
    report_status_once
    return 0
  fi
  say "Checking lanhop as it runs at login."
  told=
  previous=
  begun=$(seconds_now)
  since=$begun
  while :; do
    line=$(current_status)
    state=${line%%:*}
    now=$(seconds_now)
    if [ "$state" != "$previous" ]; then
      previous=$state
      since=$now
    fi
    in_state=$((now - since))
    case $state in
      connected | sharing)
        say ""
        say "$line"
        return 0
        ;;
      accessibility) ask_for_accessibility ;;
      connecting) warn_about_the_local_network_alert ;;
      blocked)
        warn_about_the_local_network_alert
        if [ "$os" != darwin ]; then
          tell_once_and_wait blocked "$line"
        elif [ "$in_state" -ge "$blocked_patience" ]; then
          ask_for_local_network
        fi
        ;;
      unreachable | failing) tell_once_and_wait "$state" "$line" ;;
      stopped | silent)
        if [ "$in_state" -ge "$stopped_patience" ]; then
          say ""
          say "$line"
          return 0
        fi
        ;;
      waiting)
        if [ "$in_state" -ge "$central_wait" ]; then
          say ""
          say "lanhop is ready on this machine, and no other machine is connected yet. The installer on each of them says so when it connects."
          return 0
        fi
        ;;
    esac
    if [ $((now - begun)) -ge "$check_limit" ]; then
      say ""
      say "lanhop is not connected yet. \"lanhop status\" says at any time what lanhop is doing. Now:"
      say "$line"
      return 0
    fi
    sleep 1
  done
}

explain_layout() {
  say ""
  if ! "$binary" help 2>/dev/null | grep -q -e 'layout --edit'; then
    say "To switch by pushing the cursor through an edge of the screen, write where each machine sits in the layout file, by hand:"
    say ""
    lanhop_as_the_service_runs_it layout || true
    return 0
  fi
  say "To switch by pushing the cursor through an edge of the screen, say where each machine sits: \"lanhop layout --edit\" opens a page to arrange them by dragging."
  if [ "$someone_answers" = yes ] && agrees "Open it now?"; then
    outliving_ctrl_c lanhop_as_the_service_runs_it layout --edit <&3 || problem "the editor did not finish. Run \"lanhop layout --edit\" again."
  fi
}

install_lanhop() {
  check_role
  install_binary
  advise_about_path
  explain_earlier_program
  choose_role
  if [ -z "$role" ]; then
    explain_what_is_left
    return 0
  fi
  if [ "$os" = darwin ]; then
    set_up_macos
  else
    set_up_linux
  fi
  explain_pairing
  if [ "$role" = share ]; then
    explain_layout
  fi
  say ""
  say "To remove lanhop: $install_command -s -- --uninstall"
  check_service
}

disable_unit() {
  system systemctl --user disable --now "$unit_name"
}

remove_unit() {
  if [ ! -e "$unit_path" ]; then
    return 0
  fi
  disable_unit || problem "systemd did not stop lanhop. Stop it by hand: pkill lanhop"
  rm -f "$unit_path"
  system systemctl --user daemon-reload || true
  say "Removed $unit_path"
}

delete_udev_rule() {
  system sudo rm -f "$udev_rule_path" "$uinput_at_boot_path" &&
    system sudo udevadm control --reload-rules
}

remove_udev_rule() {
  if [ ! -e "$rule_record" ]; then
    return 0
  fi
  say ""
  say "The installer wrote the rule that lets the \"input\" group use /dev/uinput. These commands remove it:"
  say ""
  shown delete_udev_rule
  say ""
  if ! agrees "Run them now?"; then
    say "Not run. The rule stays."
  elif delete_udev_rule; then
    rm -f "$rule_record"
    rmdir "$(dirname "$rule_record")" 2>/dev/null || true
    say "Removed $udev_rule_path"
  else
    problem "that did not work. The rule stays."
  fi
}

remove_agent() {
  if [ ! -e "$agent_path" ]; then
    return 0
  fi
  user_id=$(id -u)
  stop_agent 2>/dev/null || true
  rm -f "$agent_path" "$log_path"
  say "Removed $agent_path"
}

is_the_app() {
  [ -f "$1/Contents/Info.plist" ] && grep -qF "<string>$app_identifier</string>" "$1/Contents/Info.plist"
}

remove_apps() {
  for candidate in "$system_root/Applications/lanhop.app" "$HOME/Applications/lanhop.app"; do
    rm -rf "$candidate".new.* "$candidate".old.* 2>/dev/null || true
    if ! is_the_app "$candidate"; then
      continue
    fi
    if [ ! -w "$(dirname "$candidate")" ]; then
      say "Left $candidate, which this user cannot remove."
    elif rm -rf "$candidate"; then
      say "Removed $candidate"
    else
      problem "could not remove $candidate"
    fi
  done
}

uninstall_lanhop() {
  if [ "$os" = darwin ]; then
    remove_agent
    remove_apps
  else
    remove_unit
    remove_udev_rule
  fi
  rm -f "$config_directory/status" "$config_directory/status.lock"
  if [ -e "$binary" ] || [ -L "$binary" ]; then
    rm -f "$binary"
    say "Removed $binary"
  else
    say "There is no lanhop in $install_directory"
  fi
  say ""
  say "The key of this machine and its trusted machines are kept in $config_directory"
  say "Remove that directory to forget them."
  if [ "$os" = darwin ]; then
    say "To take the permissions back, remove lanhop from System Settings > Privacy & Security > Accessibility, and switch it off under Local Network."
  else
    say "If the installer added you to the input group, leave it with: sudo gpasswd -d $(id -un) input"
  fi
}

main() {
  action=install
  for argument in "$@"; do
    case $argument in
      --uninstall) action=uninstall ;;
      --help | -h)
        usage
        exit 0
        ;;
      *)
        problem "unknown option $argument"
        usage >&2
        exit 2
        ;;
    esac
  done
  find_system
  settle_paths
  open_terminal
  if [ "$action" = uninstall ]; then
    uninstall_lanhop
  else
    install_lanhop
  fi
}

main "$@"
