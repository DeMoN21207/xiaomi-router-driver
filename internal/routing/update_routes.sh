#!/bin/sh

# --- SETTINGS ---
DOMAIN_LIST="${DOMAIN_LIST:-domains.list}"
VPN_GATEWAY="${VPN_GATEWAY:-10.8.0.1}"
VPN_ROUTE_MODE="${VPN_ROUTE_MODE:-gateway}"
VPN_MASQUERADE="${VPN_MASQUERADE:-1}"

# --- IMPORTANT INTERFACE SETTINGS ---
# Run `ip a` on your router to see interface names.
LAN_IFACE="${LAN_IFACE:-br-lan}"      # Your router's LAN interface (e.g. br-lan, br0, eth1)
VPN_IFACE="${VPN_IFACE:-tun0}"        # Your VPN client's tunnel interface (e.g. tun0)
DOCKER_IFACE="${DOCKER_IFACE:-br-docker}" # Optional Docker bridge whose containers may need the same routed domains.

# --- Advanced settings ---
TABLE_NUM="${TABLE_NUM:-101}"
FW_ZONE_CHAIN="${FW_ZONE_CHAIN:-zone_lan_forward}" # The firewall chain for the LAN zone
IPSET_NAME="${IPSET_NAME:-vpn_hosts}"
FWMARK="${FWMARK:-0x1}"
DNSMASQ_CONFIG_FILE="${DNSMASQ_CONFIG_FILE:-/tmp/dnsmasq.d/vpn_dns.conf}"
DNSMASQ_MAIN_CONFIG="${DNSMASQ_MAIN_CONFIG:-/etc/dnsmasq.conf}"
DNS_PROXY_SERVER="${DNS_PROXY_SERVER:-}"
DOMAIN_STATS_CHAIN="${DOMAIN_STATS_CHAIN:-VDS_${IPSET_NAME}}"
LEGACY_DOMAIN_STATS_CHAIN="${LEGACY_DOMAIN_STATS_CHAIN:-VPN_DOM_STATS_${IPSET_NAME}}"
DNSMASQ_RESTART_LOG="${DNSMASQ_RESTART_LOG:-/tmp/vpn-manager-dnsmasq-restart.log}"
PRIME_MAX_DOMAINS="${PRIME_MAX_DOMAINS:-0}"
DNS_PRIME_LOOKUP_TIMEOUT_SECONDS="${DNS_PRIME_LOOKUP_TIMEOUT_SECONDS:-2}"
DNS_PRIME_SERVERS="${DNS_PRIME_SERVERS:-1.1.1.1,8.8.8.8,9.9.9.9}"
DOMAIN_STATS_MAX_DOMAINS="${DOMAIN_STATS_MAX_DOMAINS:-128}"
IPSET_TIMEOUT="${IPSET_TIMEOUT:-1800}"
IPSET_RESTORE_FILE="${IPSET_RESTORE_FILE:-}"
FAST_RECOVERY="${FAST_RECOVERY:-0}"
IPSET_FLUSH_ON_SYNC="${IPSET_FLUSH_ON_SYNC:-0}"
ROUTING_LOCK_FILE="${ROUTING_LOCK_FILE:-/tmp/vpn-manager-routing.lock}"
ROUTING_LOCK_WAIT_SECONDS="${ROUTING_LOCK_WAIT_SECONDS:-30}"
IPTABLES_WAIT_SECONDS="${IPTABLES_WAIT_SECONDS:-5}"
MSS_CLAMP="${MSS_CLAMP:-1}"
MSS_VALUE="${MSS_VALUE:-0}" # 0 means --clamp-mss-to-pmtu
DNS_HIJACK="${DNS_HIJACK:-1}"
IPV6_MODE="${IPV6_MODE:-warn}" # warn | allow | disable
SAFE_DIRECT_ON_ROUTE_FAIL="${SAFE_DIRECT_ON_ROUTE_FAIL:-0}"
GUARD_IPSET="vpn_manager_guard"
GUARD_CHAIN="VPN_MANAGER_GUARD"
EGRESS_GUARD_CHAIN="VPN_MANAGER_EGRESS"
GUARD_STATE_FILE="${GUARD_STATE_FILE:-}"
PINNED_IPSETS="${PINNED_IPSETS:-}"
PINNED_CHAIN="VPN_MANAGER_PINNED"
PINNED_FILTER_CHAIN="VPN_MANAGER_PIN_GUARD"
PINNED_STATE_DIR="${GUARD_STATE_FILE:-/tmp/vpn-manager-guard}.pinned"
CONNTRACK_FLUSH_ON_APPLY="${CONNTRACK_FLUSH_ON_APPLY:-0}"
# --- END OF SETTINGS ---

ENABLE_DOMAIN_STATS=1
DNSMASQ_CONFIG_CHANGED=0
DNSMASQ_CONFIG_BASENAME="${DNSMASQ_CONFIG_FILE##*/}"
DNSMASQ_CONFIG_BACKUP="${DNSMASQ_CONFIG_BACKUP:-/tmp/vpn-manager-dnsmasq/${DNSMASQ_CONFIG_BASENAME}.bak}"

short_hash() {
    value="$1"
    hash=""
    if command -v md5sum >/dev/null 2>&1; then
        hash=$(printf '%s' "$value" | md5sum 2>/dev/null | cut -c1-8)
    fi
    if [ -z "$hash" ] && command -v cksum >/dev/null 2>&1; then
        hash=$(printf '%s' "$value" | cksum | cut -d' ' -f1 | cut -c1-8)
    fi
    if [ -z "$hash" ]; then
        hash=$(printf '%s' "$value" | tr -cd '[:alnum:]' | cut -c1-8)
    fi
    if [ -z "$hash" ]; then
        hash="00000000"
    fi
    printf '%s' "$hash"
}

legacy_domain_prefix="vpn_d_${IPSET_NAME}_"
compact_domain_prefix="vd_$(short_hash "$IPSET_NAME")_"
MSS_CHAIN="VMS_$(short_hash "${IPSET_NAME}:${FWMARK}")"

acquire_routing_lock() {
    if command -v flock >/dev/null 2>&1; then
        exec 9>"$ROUTING_LOCK_FILE" || return 1
        if flock -w "$ROUTING_LOCK_WAIT_SECONDS" 9 >/dev/null 2>&1; then
            ROUTING_LOCK_KIND="flock"
            return 0
        fi
        if flock -n 9 >/dev/null 2>&1; then
            ROUTING_LOCK_KIND="flock"
            return 0
        fi
        echo "Error: another routing update is already running." >&2
        return 1
    fi

    ROUTING_LOCK_DIR="${ROUTING_LOCK_FILE}.d"
    deadline=$(( $(date +%s 2>/dev/null || echo 0) + ROUTING_LOCK_WAIT_SECONDS ))
    while ! mkdir "$ROUTING_LOCK_DIR" >/dev/null 2>&1; do
        now=$(date +%s 2>/dev/null || echo 0)
        if [ "$now" -ge "$deadline" ]; then
            echo "Error: another routing update is already running." >&2
            return 1
        fi
        sleep 1
    done
    ROUTING_LOCK_KIND="dir"
    return 0
}

release_routing_lock() {
    if [ "$ROUTING_LOCK_KIND" = "dir" ] && [ -n "$ROUTING_LOCK_DIR" ]; then
        rmdir "$ROUTING_LOCK_DIR" >/dev/null 2>&1 || true
    fi
    if [ "$ROUTING_LOCK_KIND" = "flock" ]; then
        exec 9>&- 2>/dev/null || true
    fi
}

setup_iptables_wait() {
    IPTABLES_HAS_WAIT=0
    if command iptables -w "$IPTABLES_WAIT_SECONDS" -L -n >/dev/null 2>&1; then
        IPTABLES_HAS_WAIT=1
    fi
}

iptables() {
    if [ "$IPTABLES_HAS_WAIT" = "1" ]; then
        command iptables -w "$IPTABLES_WAIT_SECONDS" "$@"
    else
        command iptables "$@"
    fi
}

run_with_timeout() {
    seconds="$1"
    shift

    if ! command -v timeout >/dev/null 2>&1 || [ "${seconds:-0}" -le 0 ] 2>/dev/null; then
        "$@"
        return
    fi

    if [ "${TIMEOUT_NEEDS_T_FLAG:-}" = "" ]; then
        if timeout --help 2>&1 | grep -q -- '\[-t SECS\]'; then
            TIMEOUT_NEEDS_T_FLAG=1
        else
            TIMEOUT_NEEDS_T_FLAG=0
        fi
    fi

    if [ "$TIMEOUT_NEEDS_T_FLAG" = "1" ]; then
        timeout -t "$seconds" "$@"
    else
        timeout "$seconds" "$@"
    fi
}

resolve_domain_ips() {
    domain="$1"
    dns_server="${2:-127.0.0.1}"
    if command -v timeout >/dev/null 2>&1 && [ "${DNS_PRIME_LOOKUP_TIMEOUT_SECONDS:-0}" -gt 0 ] 2>/dev/null; then
        run_with_timeout "$DNS_PRIME_LOOKUP_TIMEOUT_SECONDS" nslookup "$domain" "$dns_server" 2>/dev/null
    else
        nslookup "$domain" "$dns_server" 2>/dev/null
    fi \
        | awk '/^Name:/ {capture=1; next} capture && /^Address [0-9]+: / {print $3} capture && /^Address: / {print $2}' \
        | grep -E '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$' \
        | sort -u
}

prime_domain_ips() {
    domain="$1"
    collected=""

    for ip in $(resolve_domain_ips "$domain" "127.0.0.1"); do
        case " $collected " in
            *" $ip "*) ;;
            *)
                collected="$collected $ip"
                ipset add "$IPSET_NAME" "$ip" timeout "$IPSET_TIMEOUT" -exist >/dev/null 2>&1
                if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
                    dom_hash=$(short_hash "${IPSET_NAME}:${domain}")
                    dom_set="${compact_domain_prefix}${dom_hash}"
                    ipset add "$dom_set" "$ip" timeout "$IPSET_TIMEOUT" -exist >/dev/null 2>&1
                fi
                ;;
        esac
    done

    for dns_server in $(printf '%s\n' "$DNS_PRIME_SERVERS" | tr ',;' ' '); do
        dns_server=$(echo "$dns_server" | tr -d '\r')
        if [ -z "$dns_server" ] || [ "$dns_server" = "127.0.0.1" ]; then
            continue
        fi

        for ip in $(resolve_domain_ips "$domain" "$dns_server"); do
            case " $collected " in
                *" $ip "*) ;;
                *)
                    collected="$collected $ip"
                    ipset add "$IPSET_NAME" "$ip" timeout "$IPSET_TIMEOUT" -exist >/dev/null 2>&1
                    if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
                        dom_hash=$(short_hash "${IPSET_NAME}:${domain}")
                        dom_set="${compact_domain_prefix}${dom_hash}"
                        ipset add "$dom_set" "$ip" timeout "$IPSET_TIMEOUT" -exist >/dev/null 2>&1
                    fi
                    ;;
            esac
        done
    done
}

domain_stats_enabled() {
    domain_count="$1"
    mode="$(printf '%s' "${VPN_MANAGER_DOMAIN_STATS:-${DOMAIN_STATS:-}}" | tr 'A-Z' 'a-z')"
    case "$mode" in
        0|off|false|disabled)
            return 1
            ;;
        1|on|true|enabled)
            return 0
            ;;
    esac

    if [ "${DOMAIN_STATS_MAX_DOMAINS:-0}" -le 0 ] 2>/dev/null; then
        return 0
    fi
    [ "$domain_count" -le "$DOMAIN_STATS_MAX_DOMAINS" ]
}

dnsmasq_control() {
    action="$1"
    mkdir -p "$(dirname "$DNSMASQ_RESTART_LOG")"
    : > "$DNSMASQ_RESTART_LOG"

    if [ -x /etc/init.d/dnsmasq ]; then
        /etc/init.d/dnsmasq "$action" >/dev/null 2>"$DNSMASQ_RESTART_LOG"
        return $?
    fi

    if [ "$action" = "reload" ] || [ "$action" = "restart" ]; then
        if command -v pidof >/dev/null 2>&1; then
            pids=$(pidof dnsmasq 2>/dev/null)
            if [ -n "$pids" ]; then
                kill -HUP $pids >/dev/null 2>"$DNSMASQ_RESTART_LOG"
                return $?
            fi
        fi
    fi

    echo "dnsmasq control path not found" > "$DNSMASQ_RESTART_LOG"
    return 1
}

reload_dnsmasq() {
    mode="${DNSMASQ_RELOAD_MODE:-auto}"
    case "$mode" in
        restart)
            dnsmasq_control restart
            return $?
            ;;
        reload)
            dnsmasq_control reload
            return $?
            ;;
    esac

    if dnsmasq_control reload; then
        return 0
    fi
    if command -v pidof >/dev/null 2>&1; then
        pids=$(pidof dnsmasq 2>/dev/null)
        if [ -n "$pids" ] && kill -HUP $pids >/dev/null 2>>"$DNSMASQ_RESTART_LOG"; then
            return 0
        fi
    fi
    dnsmasq_control restart
}

apply_dnsmasq_changes() {
    if [ "$DNSMASQ_CONFIG_CHANGED" != "1" ]; then
        echo "--> dnsmasq config unchanged; reload skipped."
        return 0
    fi

    echo "--> Restarting dnsmasq to apply config..."
    # Some OpenWrt dnsmasq builds do not reread conf-dir snippets on reload/SIGHUP.
    if dnsmasq_control restart; then
        return 0
    fi

    echo "Error: dnsmasq restart failed." >&2
    if [ -s "$DNSMASQ_RESTART_LOG" ]; then
        cat "$DNSMASQ_RESTART_LOG" >&2
    fi

    if [ -f "$DNSMASQ_CONFIG_BACKUP" ]; then
        echo "--> Restoring previous dnsmasq config after failed restart." >&2
        cp "$DNSMASQ_CONFIG_BACKUP" "$DNSMASQ_CONFIG_FILE" >/dev/null 2>&1 || true
        dnsmasq_control restart >/dev/null 2>&1 || true
    fi
    return 1
}

count_active_domains() {
    grep -vE '^\s*#|^\s*$' "$DOMAIN_LIST" | wc -l | tr -d ' '
}

prime_ipsets() {
    if [ -n "$IPSET_RESTORE_FILE" ]; then
        if ! ipset restore -exist < "$IPSET_RESTORE_FILE"; then
            echo "Error: failed to restore known VPN destinations." >&2
            exit 1
        fi
    fi
    if [ "${PRIME_MAX_DOMAINS:-0}" -le 0 ] 2>/dev/null; then
        echo "--> Priming disabled; dnsmasq/ipset will populate entries on demand."
        return 0
    fi

    count=0

    while IFS= read -r domain_raw || [ -n "$domain_raw" ]; do
        domain=$(echo "$domain_raw" | tr -d '\r')
        case "$domain" in
            ""|\#*) continue ;;
        esac
        if is_ipv4_entry "$domain"; then
            continue
        fi

        count=$((count + 1))
        if [ "${PRIME_MAX_DOMAINS:-0}" -gt 0 ] && [ "$count" -gt "$PRIME_MAX_DOMAINS" ]; then
            echo "--> Priming capped at ${PRIME_MAX_DOMAINS} domains; remaining entries will populate dynamically."
            break
        fi

        prime_domain_ips "$domain"
    done < "$DOMAIN_LIST"
}

is_ipv4_entry() {
    printf '%s' "$1" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+(/[0-9]{1,2})?$'
}

count_domain_entries() {
    count=0
    while IFS= read -r entry_raw || [ -n "$entry_raw" ]; do
        entry=$(echo "$entry_raw" | tr -d '\r')
        case "$entry" in
            ""|\#*) continue ;;
        esac
        if is_ipv4_entry "$entry"; then
            continue
        fi
        count=$((count + 1))
    done < "$DOMAIN_LIST"
    printf '%s' "$count"
}

prime_static_entries_fallback() {
    while IFS= read -r entry_raw || [ -n "$entry_raw" ]; do
        entry=$(echo "$entry_raw" | tr -d '\r')
        case "$entry" in
            ""|\#*) continue ;;
        esac
        if ! is_ipv4_entry "$entry"; then
            continue
        fi

        ipset add "$IPSET_NAME" "$entry" timeout 0 -exist >/dev/null 2>&1
    done < "$DOMAIN_LIST"
}

prime_static_entries_batch() {
    tmp_restore="/tmp/vpn-manager-ipset.$$"
    add_count=0

    : > "$tmp_restore" || return 1
    printf 'create %s hash:net family inet timeout %s -exist\n' "$IPSET_NAME" "$IPSET_TIMEOUT" >> "$tmp_restore" || {
        rm -f "$tmp_restore"
        return 1
    }

    while IFS= read -r entry_raw || [ -n "$entry_raw" ]; do
        entry=$(echo "$entry_raw" | tr -d '\r')
        case "$entry" in
            ""|\#*) continue ;;
        esac
        if ! is_ipv4_entry "$entry"; then
            continue
        fi

        printf 'add %s %s timeout 0 -exist\n' "$IPSET_NAME" "$entry" >> "$tmp_restore" || {
            rm -f "$tmp_restore"
            return 1
        }
        add_count=$((add_count + 1))
    done < "$DOMAIN_LIST"

    if [ "$add_count" -eq 0 ]; then
        rm -f "$tmp_restore"
        return 0
    fi

    if ipset restore -exist < "$tmp_restore" >/dev/null 2>&1; then
        rm -f "$tmp_restore"
        return 0
    fi

    rm -f "$tmp_restore"
    return 1
}

prime_static_entries() {
    if prime_static_entries_batch; then
        return 0
    fi

    echo "Warning: batch ipset restore failed for static entries; falling back to per-entry add." >&2
    prime_static_entries_fallback
}

shared_ipset_ready() {
    if ! ipset list "$IPSET_NAME" >/dev/null 2>&1; then
        return 1
    fi
    if ! ipset list "$IPSET_NAME" 2>/dev/null | grep -F "Type: hash:net" >/dev/null 2>&1; then
        return 1
    fi
    return 0
}

ensure_shared_ipset() {
    if shared_ipset_ready; then
        ipset flush "$IPSET_NAME" >/dev/null 2>&1 || return 1
        return 0
    fi

    ipset destroy "$IPSET_NAME" >/dev/null 2>&1 || true
    ipset create "$IPSET_NAME" hash:net family inet timeout "$IPSET_TIMEOUT" -exist
}

# Independent of runtime tables and their expiring DNS sets. Never cleared by
# add/sync/del: a stopped process or an interrupted replacement cannot release
# a previously protected destination to WAN. One bounded set for all routes.
ensure_guard() {
    guard_new=0
    ipset list "$GUARD_IPSET" >/dev/null 2>&1 || guard_new=1
    ipset create "$GUARD_IPSET" hash:net family inet timeout 0 maxelem 65536 -exist || return 1
    if [ "$guard_new" = "1" ] && [ -n "$GUARD_STATE_FILE" ] && [ -f "$GUARD_STATE_FILE" ]; then
        ipset restore -exist < "$GUARD_STATE_FILE" || return 1
    fi
    iptables -N "$GUARD_CHAIN" 2>/dev/null || true
    iptables -t mangle -N "$EGRESS_GUARD_CHAIN" 2>/dev/null || true
    iptables -N "$PINNED_FILTER_CHAIN" 2>/dev/null || true
    iptables -t mangle -N "$PINNED_FILTER_CHAIN" 2>/dev/null || true
    if ! iptables -C "$GUARD_CHAIN" -j "$PINNED_FILTER_CHAIN" >/dev/null 2>&1; then
        iptables -I "$GUARD_CHAIN" 1 -j "$PINNED_FILTER_CHAIN" || return 1
    fi
    if ! iptables -t mangle -C "$EGRESS_GUARD_CHAIN" -j "$PINNED_FILTER_CHAIN" >/dev/null 2>&1; then
        iptables -t mangle -I "$EGRESS_GUARD_CHAIN" 1 -j "$PINNED_FILTER_CHAIN" || return 1
    fi
    if ! iptables -t mangle -C "$EGRESS_GUARD_CHAIN" -o 'sb+' -j RETURN >/dev/null 2>&1; then
        iptables -t mangle -I "$EGRESS_GUARD_CHAIN" 2 -o 'sb+' -j RETURN || return 1
    fi
    # Return traffic to the LAN is unaffected; protected destinations may leave
    # only through managed tunnel devices. Rules precede ESTABLISHED accepts.
    if ! iptables -C "$GUARD_CHAIN" -o 'sb+' -j RETURN >/dev/null 2>&1; then
        iptables -I "$GUARD_CHAIN" 2 -o 'sb+' -j RETURN || return 1
    fi
    case "$VPN_IFACE" in
        sb*) ;;
        *) if ! iptables -C "$GUARD_CHAIN" -o "$VPN_IFACE" -j RETURN >/dev/null 2>&1; then
               iptables -I "$GUARD_CHAIN" 2 -o "$VPN_IFACE" -j RETURN || return 1
           fi
           if ! iptables -t mangle -C "$EGRESS_GUARD_CHAIN" -o "$VPN_IFACE" -j RETURN >/dev/null 2>&1; then
               iptables -t mangle -I "$EGRESS_GUARD_CHAIN" 2 -o "$VPN_IFACE" -j RETURN || return 1
           fi ;;
    esac
    if ! iptables -C "$GUARD_CHAIN" -m set --match-set "$GUARD_IPSET" dst -j REJECT >/dev/null 2>&1; then
        iptables -A "$GUARD_CHAIN" -m set --match-set "$GUARD_IPSET" dst -j REJECT || return 1
    fi
    if ! iptables -t mangle -C "$EGRESS_GUARD_CHAIN" -m set --match-set "$GUARD_IPSET" dst -j DROP >/dev/null 2>&1; then
        iptables -t mangle -A "$EGRESS_GUARD_CHAIN" -m set --match-set "$GUARD_IPSET" dst -j DROP || return 1
    fi
    # Also protect marked packets even if their destination set was cleared.
    # Tunnel RETURN rules above remain the only permitted output for them.
    guard_marks="$FWMARK"
    if [ -n "$GUARD_STATE_FILE" ] && [ -r "${GUARD_STATE_FILE}.marks" ]; then
        guard_marks="$guard_marks $(cat "${GUARD_STATE_FILE}.marks")"
    fi
    for guard_mark in $guard_marks; do
        [ "$guard_mark" = "0" ] || [ "$guard_mark" = "0x0" ] && continue
        if ! iptables -C "$GUARD_CHAIN" -m mark --mark "$guard_mark" -j REJECT >/dev/null 2>&1; then
            iptables -A "$GUARD_CHAIN" -m mark --mark "$guard_mark" -j REJECT || return 1
        fi
        if ! iptables -t mangle -C "$EGRESS_GUARD_CHAIN" -m mark --mark "$guard_mark" -j DROP >/dev/null 2>&1; then
            iptables -t mangle -A "$EGRESS_GUARD_CHAIN" -m mark --mark "$guard_mark" -j DROP || return 1
        fi
    done
    if [ -n "$GUARD_STATE_FILE" ]; then
        mkdir -p "$(dirname "$GUARD_STATE_FILE")" || return 1
        printf '%s\n' $guard_marks | sort -u > "${GUARD_STATE_FILE}.marks.tmp" &&
            mv "${GUARD_STATE_FILE}.marks.tmp" "${GUARD_STATE_FILE}.marks" || return 1
    fi
    for guard_parent in FORWARD; do
        if ! iptables -C "$guard_parent" -j "$GUARD_CHAIN" >/dev/null 2>&1; then
            iptables -I "$guard_parent" 1 -j "$GUARD_CHAIN" || return 1
        fi
    done
    # OUTPUT's -o may still name the original interface after mangle changes a
    # mark. POSTROUTING sees the final interface, and covers both LAN and local
    # packets. Install it before retiring the legacy OUTPUT check.
    if ! iptables -t mangle -C POSTROUTING -j "$EGRESS_GUARD_CHAIN" >/dev/null 2>&1; then
        iptables -t mangle -I POSTROUTING 1 -j "$EGRESS_GUARD_CHAIN" || return 1
    fi
    while iptables -D OUTPUT -j "$GUARD_CHAIN" >/dev/null 2>&1; do :; done
}

# Fixed routes remember destinations independently of expiring routing sets.
# Apply their marks after ordinary marks; reject an exit through any other TUN.
ensure_pinned_routes() {
    mkdir -p "$PINNED_STATE_DIR" || return 1
    iptables -t mangle -N "$PINNED_CHAIN" 2>/dev/null || true
    for pin_set in $(printf '%s' "$PINNED_IPSETS" | tr ',' ' '); do
        case "$pin_set" in vpn_pin_*) ;; *) return 1 ;; esac
        ipset create "$pin_set" hash:net family inet timeout 0 maxelem 65536 -exist || return 1
        if ! iptables -t mangle -C "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$VPN_IFACE" -j DROP >/dev/null 2>&1; then
            iptables -t mangle -I "$PINNED_FILTER_CHAIN" 1 -m set --match-set "$pin_set" dst ! -o "$VPN_IFACE" -j DROP || return 1
        fi
        if [ -f "$PINNED_STATE_DIR/$pin_set.restore" ]; then
            ipset restore -exist < "$PINNED_STATE_DIR/$pin_set.restore" || return 1
        fi
        if ipset list "$IPSET_NAME" >/dev/null 2>&1; then
            ipset save "$IPSET_NAME" | awk -v target="$pin_set" '$1 == "add" {print "add", target, $3, "timeout 0"}' | ipset restore -exist || return 1
        fi
        if [ -f "$DOMAIN_LIST" ]; then
            while IFS= read -r pin_entry; do
                pin_entry=$(printf '%s' "$pin_entry" | tr -d '\r')
                if is_ipv4_entry "$pin_entry"; then
                    ipset add "$pin_set" "$pin_entry" timeout 0 -exist || return 1
                fi
            done < "$DOMAIN_LIST"
        fi
        # Install the new constraint before retiring a previous location/mark.
        if ! iptables -C "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$VPN_IFACE" -j REJECT >/dev/null 2>&1; then
            iptables -I "$PINNED_FILTER_CHAIN" 1 -m set --match-set "$pin_set" dst ! -o "$VPN_IFACE" -j REJECT || return 1
        fi
        if [ -r "$PINNED_STATE_DIR/$pin_set.binding" ]; then
            read -r pin_old_iface pin_old_mark < "$PINNED_STATE_DIR/$pin_set.binding"
            if [ "$pin_old_iface" != "$VPN_IFACE" ]; then
                while iptables -D "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_old_iface" -j REJECT >/dev/null 2>&1; do :; done
                while iptables -t mangle -D "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_old_iface" -j DROP >/dev/null 2>&1; do :; done
            fi
            if [ "$pin_old_mark" != "$FWMARK" ]; then
                while iptables -t mangle -D "$PINNED_CHAIN" -m set --match-set "$pin_set" dst -j MARK --set-mark "$pin_old_mark" >/dev/null 2>&1; do :; done
            fi
        fi
        ipset save "$pin_set" > "$PINNED_STATE_DIR/$pin_set.restore.tmp" &&
            mv "$PINNED_STATE_DIR/$pin_set.restore.tmp" "$PINNED_STATE_DIR/$pin_set.restore" || return 1
        printf '%s %s\n' "$VPN_IFACE" "$FWMARK" > "$PINNED_STATE_DIR/$pin_set.binding.tmp" &&
            mv "$PINNED_STATE_DIR/$pin_set.binding.tmp" "$PINNED_STATE_DIR/$pin_set.binding" || return 1
    done
    for pin_binding in "$PINNED_STATE_DIR"/*.binding; do
        [ -f "$pin_binding" ] || continue
        pin_set=${pin_binding##*/}
        pin_set=${pin_set%.binding}
        read -r pin_iface pin_mark < "$pin_binding" || return 1
        if ! ipset list "$pin_set" >/dev/null 2>&1; then
            ipset restore -exist < "$PINNED_STATE_DIR/$pin_set.restore" || return 1
        fi
        ipset save "$pin_set" | awk -v target="$GUARD_IPSET" '$1 == "add" {print "add", target, $3, "timeout 0"}' | ipset restore -exist || return 1
        # Explicitly removing a fixed route or giving it a rotation policy retires
        # its country constraint. Its old addresses remain protected from WAN.
        if [ -f "${GUARD_STATE_FILE}.pinned-active" ] && ! grep -Fx "$pin_set" "${GUARD_STATE_FILE}.pinned-active" >/dev/null; then
            while iptables -D "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j REJECT >/dev/null 2>&1; do :; done
            while iptables -t mangle -D "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j DROP >/dev/null 2>&1; do :; done
            while iptables -t mangle -D "$PINNED_CHAIN" -m set --match-set "$pin_set" dst -j MARK --set-mark "$pin_mark" >/dev/null 2>&1; do :; done
            rm -f "$pin_binding" || return 1
            continue
        fi
        if ! iptables -C "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j REJECT >/dev/null 2>&1; then
            iptables -I "$PINNED_FILTER_CHAIN" 1 -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j REJECT || return 1
        fi
        if ! iptables -t mangle -C "$PINNED_FILTER_CHAIN" -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j DROP >/dev/null 2>&1; then
            iptables -t mangle -I "$PINNED_FILTER_CHAIN" 1 -m set --match-set "$pin_set" dst ! -o "$pin_iface" -j DROP || return 1
        fi
        if ! iptables -t mangle -C "$PINNED_CHAIN" -m set --match-set "$pin_set" dst -j MARK --set-mark "$pin_mark" >/dev/null 2>&1; then
            iptables -t mangle -A "$PINNED_CHAIN" -m set --match-set "$pin_set" dst -j MARK --set-mark "$pin_mark" || return 1
        fi
    done
    # Ordinary route rules are inserted at the head; pinned marks run last.
    if ! iptables -t mangle -C OUTPUT -j "$PINNED_CHAIN" >/dev/null 2>&1; then
        iptables -t mangle -A OUTPUT -j "$PINNED_CHAIN" || return 1
    fi
    for pin_lan in "$LAN_IFACE" "$DOCKER_IFACE"; do
        [ -n "$pin_lan" ] || continue
        if ! iptables -t mangle -C PREROUTING -i "$pin_lan" -j "$PINNED_CHAIN" >/dev/null 2>&1; then
            iptables -t mangle -A PREROUTING -i "$pin_lan" -j "$PINNED_CHAIN" || return 1
        fi
    done
}

ensure_firewall_processing() {
    # Qualcomm ECM/SFE can forward established flows outside netfilter. Stop
    # new acceleration and drain existing accelerated flows without clearing
    # conntrack or unloading the network drivers.
    if [ ! -d /sys/module/ecm ]; then
        return 0
    fi
    ecm_dir=/sys/kernel/debug/ecm
    ecm_changed=0
    # Vendor services can reset front_end_ipv4_stop. The independent default
    # classifier value 1 (ECM_CLASSIFIER_ACCELERATION_MODE_NO) vetoes new flows
    # even when that frontend is resumed. Value 0 means DONT_CARE, not disabled.
    for ecm_stop in "$ecm_dir/front_end_ipv4_stop" "$ecm_dir/front_end_ipv6_stop" "$ecm_dir/ecm_classifier_default/accel_mode"; do
        if [ ! -r "$ecm_stop" ] || [ ! -w "$ecm_stop" ]; then
            echo "Error: cannot disable ECM acceleration; firewall protection is not verified." >&2
            return 1
        fi
        if [ "$(cat "$ecm_stop")" != "1" ]; then
            echo 1 > "$ecm_stop" || return 1
            ecm_changed=1
        fi
    done
    if [ "$ecm_changed" = "1" ]; then
        echo 1 > "$ecm_dir/ecm_db/defunct_all" || return 1
    fi
    for ecm_attempt in 1 2 3 4 5 6; do
        ecm_active=0
        for ecm_count in "$ecm_dir"/ecm_*_ipv[46]/accelerated_count; do
            [ -r "$ecm_count" ] || continue
            ecm_active=$((ecm_active + $(cat "$ecm_count")))
        done
        [ "$ecm_active" = "0" ] && return 0
        [ "$ecm_attempt" = "6" ] && break
        echo 1 > "$ecm_dir/ecm_db/defunct_all" || return 1
        sleep 1
    done
    echo "Error: accelerated connections remain outside firewall processing." >&2
    return 1
}

protect_known_destinations() {
    ensure_guard || return 1
    ensure_pinned_routes || return 1
    ensure_firewall_processing || return 1
    handle_ipv6_mode || return 1
    ensure_dns_hijack_rules || return 1
    if ipset list "$IPSET_NAME" >/dev/null 2>&1; then
        ipset save "$IPSET_NAME" | awk -v target="$GUARD_IPSET" '$1 == "add" {print "add", target, $3, "timeout 0"}' | ipset restore -exist || return 1
    fi
    if [ -f "$DOMAIN_LIST" ]; then
        while IFS= read -r guard_entry; do
            guard_entry=$(printf '%s' "$guard_entry" | tr -d '\r')
            if is_ipv4_entry "$guard_entry"; then
                ipset add "$GUARD_IPSET" "$guard_entry" timeout 0 -exist || return 1
            fi
        done < "$DOMAIN_LIST"
    fi
    if [ -n "$GUARD_STATE_FILE" ]; then
        mkdir -p "$(dirname "$GUARD_STATE_FILE")" || return 1
        ipset save "$GUARD_IPSET" > "${GUARD_STATE_FILE}.tmp" &&
            mv "${GUARD_STATE_FILE}.tmp" "$GUARD_STATE_FILE" || return 1
    fi
}

iptables_chain_exists() {
    iptables -nL "$1" >/dev/null 2>&1
}

resolve_forward_chain() {
    if [ -n "$FW_ZONE_CHAIN" ] && iptables_chain_exists "$FW_ZONE_CHAIN"; then
        printf '%s\n' "$FW_ZONE_CHAIN"
        return 0
    fi
    printf '%s\n' "FORWARD"
}

enable_ipv4_forwarding() {
    if [ -w /proc/sys/net/ipv4/ip_forward ]; then
        echo 1 > /proc/sys/net/ipv4/ip_forward 2>/dev/null || true
    fi
}

handle_ipv6_mode() {
    case "$(printf '%s' "$IPV6_MODE" | tr 'A-Z' 'a-z')" in
        allow|off|0|false)
            return 0
            ;;
        disable|block)
            changed=0
            for path in \
                /proc/sys/net/ipv6/conf/all/disable_ipv6 \
                /proc/sys/net/ipv6/conf/default/disable_ipv6 \
                "/proc/sys/net/ipv6/conf/${LAN_IFACE}/disable_ipv6"; do
                if [ -w "$path" ]; then
                    echo 1 > "$path" 2>/dev/null || return 1
                    changed=1
                fi
            done
            if [ "$changed" = "1" ]; then
                echo "--> IPv6 disabled for routed LAN path to avoid IPv6 bypass/leaks."
            else
                if [ -d /proc/sys/net/ipv6 ]; then
                    echo "Error: cannot disable IPv6; refusing unprotected routing." >&2
                    return 1
                fi
            fi
            ;;
        warn|*)
            if [ -r /proc/sys/net/ipv6/conf/all/disable_ipv6 ]; then
                disabled=$(cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null)
                if [ "$disabled" = "0" ]; then
                    echo "Warning: IPv6 is enabled. Domain VPN routing is IPv4-only unless you disable IPv6 or add IPv6 policy routing." >&2
                fi
            fi
            ;;
    esac
}

cleanup_forward_accept_rules() {
    for chain in "$FW_ZONE_CHAIN" FORWARD; do
        if [ -z "$chain" ]; then continue; fi
        if ! iptables_chain_exists "$chain"; then continue; fi
        while iptables -D "$chain" -i "$LAN_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; do :; done
        if [ -n "$DOCKER_IFACE" ]; then
            while iptables -D "$chain" -i "$DOCKER_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; do :; done
        fi
    done
}

iface_exists() {
    iface="$1"
    [ -n "$iface" ] && ip link show "$iface" >/dev/null 2>&1
}

ensure_mangle_mark_rules() {
    if ! iptables -t mangle -C PREROUTING -i "$LAN_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
        iptables -t mangle -I PREROUTING -i "$LAN_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" || return 1
    fi

    if iface_exists "$DOCKER_IFACE"; then
        if ! iptables -t mangle -C PREROUTING -i "$DOCKER_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
            iptables -t mangle -I PREROUTING -i "$DOCKER_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" || return 1
        fi
    fi

    if ! iptables -t mangle -C OUTPUT -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
        iptables -t mangle -I OUTPUT -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" || return 1
    fi

    return 0
}

cleanup_mangle_mark_rules() {
    while iptables -t mangle -D PREROUTING -i "$LAN_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; do :; done
    if [ -n "$DOCKER_IFACE" ]; then
        while iptables -t mangle -D PREROUTING -i "$DOCKER_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; do :; done
    fi
    while iptables -t mangle -D OUTPUT -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; do :; done
}

cleanup_dns_hijack_rules() {
    while iptables -t nat -D PREROUTING -i "$LAN_IFACE" -p udp --dport 53 -j REDIRECT --to-ports 53 >/dev/null 2>&1; do :; done
    while iptables -t nat -D PREROUTING -i "$LAN_IFACE" -p tcp --dport 53 -j REDIRECT --to-ports 53 >/dev/null 2>&1; do :; done
}

ensure_dns_hijack_rules() {
    if [ "$DNS_HIJACK" != "1" ]; then
        return 0
    fi
    if ! iptables -t nat -C PREROUTING -i "$LAN_IFACE" -p udp --dport 53 -j REDIRECT --to-ports 53 >/dev/null 2>&1; then
        iptables -t nat -I PREROUTING -i "$LAN_IFACE" -p udp --dport 53 -j REDIRECT --to-ports 53 || return 1
    fi
    if ! iptables -t nat -C PREROUTING -i "$LAN_IFACE" -p tcp --dport 53 -j REDIRECT --to-ports 53 >/dev/null 2>&1; then
        iptables -t nat -I PREROUTING -i "$LAN_IFACE" -p tcp --dport 53 -j REDIRECT --to-ports 53 || return 1
    fi
    return 0
}

cleanup_mss_clamp_rules() {
    while iptables -t mangle -D FORWARD -j "$MSS_CHAIN" >/dev/null 2>&1; do :; done
    iptables -t mangle -F "$MSS_CHAIN" >/dev/null 2>&1 || true
    iptables -t mangle -X "$MSS_CHAIN" >/dev/null 2>&1 || true
}

append_mss_rule() {
    direction="$1"
    if [ "$direction" = "out" ]; then
        iface_arg="-o"
    else
        iface_arg="-i"
    fi

    if [ "${MSS_VALUE:-0}" -gt 0 ] 2>/dev/null; then
        iptables -t mangle -A "$MSS_CHAIN" "$iface_arg" "$VPN_IFACE" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --set-mss "$MSS_VALUE"
    else
        iptables -t mangle -A "$MSS_CHAIN" "$iface_arg" "$VPN_IFACE" -p tcp --tcp-flags SYN,RST SYN -j TCPMSS --clamp-mss-to-pmtu
    fi
}

ensure_mss_clamp_rules() {
    if [ "$MSS_CLAMP" != "1" ]; then
        return 0
    fi

    cleanup_mss_clamp_rules
    iptables -t mangle -N "$MSS_CHAIN" 2>/dev/null || true
    append_mss_rule out || return 1
    append_mss_rule in || return 1

    if ! iptables -t mangle -C FORWARD -j "$MSS_CHAIN" >/dev/null 2>&1; then
        iptables -t mangle -I FORWARD -j "$MSS_CHAIN" || return 1
    fi
    return 0
}

cleanup_domain_stats_jumps() {
    stats_chain="$1"
    for chain in FORWARD "$FW_ZONE_CHAIN"; do
        if [ -z "$chain" ]; then continue; fi
        if ! iptables_chain_exists "$chain"; then continue; fi
        while iptables -D "$chain" -o "$VPN_IFACE" -j "$stats_chain" >/dev/null 2>&1; do :; done
        while iptables -D "$chain" -i "$VPN_IFACE" -j "$stats_chain" >/dev/null 2>&1; do :; done
    done
}

ensure_domain_stats_jumps() {
    if [ "$ENABLE_DOMAIN_STATS" != "1" ]; then
        return 0
    fi
    if ! iptables -C FORWARD -o "$VPN_IFACE" -j "$DOMAIN_STATS_CHAIN" >/dev/null 2>&1; then
        iptables -I FORWARD -o "$VPN_IFACE" -j "$DOMAIN_STATS_CHAIN" 2>/dev/null || return 1
    fi
    if ! iptables -C FORWARD -i "$VPN_IFACE" -j "$DOMAIN_STATS_CHAIN" >/dev/null 2>&1; then
        iptables -I FORWARD -i "$VPN_IFACE" -j "$DOMAIN_STATS_CHAIN" 2>/dev/null || return 1
    fi
    return 0
}

ensure_vpn_route() {
    # This route remains when the TUN disappears. Never fall through to main.
    ip route replace blackhole default metric 32760 table "$TABLE_NUM" || return 1
    if ! ip link show "$VPN_IFACE" >/dev/null 2>&1; then
        echo "Error: VPN interface '$VPN_IFACE' is not present; refusing to mark traffic into a dead route." >&2
        return 1
    fi

    if [ "$VPN_ROUTE_MODE" = "dev" ]; then
        ip route replace default dev "$VPN_IFACE" metric 10 table "$TABLE_NUM"
        return $?
    fi

    ip route replace default via "$VPN_GATEWAY" dev "$VPN_IFACE" metric 10 table "$TABLE_NUM"
}

safe_direct_after_route_failure() {
    echo "--> VPN route unavailable; keeping marks and the WAN guard (fail closed)." >&2
}

validate_vpn_route_or_safe_direct() {
    if ensure_vpn_route; then
        return 0
    fi
    safe_direct_after_route_failure
    return 1
}

base_routing_ready() {
    forward_chain=$(resolve_forward_chain)
    if ! ip link show "$VPN_IFACE" >/dev/null 2>&1; then
        return 1
    fi
    if ! iptables -t mangle -C PREROUTING -i "$LAN_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
        return 1
    fi
    if ! iptables -t mangle -C OUTPUT -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
        return 1
    fi
    if iface_exists "$DOCKER_IFACE"; then
        if ! iptables -t mangle -C PREROUTING -i "$DOCKER_IFACE" -m set --match-set "$IPSET_NAME" dst -j MARK --set-mark "$FWMARK" >/dev/null 2>&1; then
            return 1
        fi
        if ! iptables -C FORWARD -i "$DOCKER_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; then
            return 1
        fi
    fi
    if ! iptables -C "$forward_chain" -i "$LAN_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; then
        return 1
    fi
    if [ "$VPN_MASQUERADE" = "1" ] && ! iptables -t nat -C POSTROUTING -o "$VPN_IFACE" -j MASQUERADE >/dev/null 2>&1; then
        return 1
    fi

    if [ "$VPN_ROUTE_MODE" = "dev" ]; then
        route_match="default dev $VPN_IFACE"
    else
        route_match="default via $VPN_GATEWAY dev $VPN_IFACE"
    fi
    if ! ip route show table "$TABLE_NUM" 2>/dev/null | grep -F "$route_match" >/dev/null 2>&1; then
        return 1
    fi
    if ! ip rule show 2>/dev/null | grep -F "fwmark $FWMARK lookup $TABLE_NUM" >/dev/null 2>&1; then
        return 1
    fi

    return 0
}

cleanup_domain_stats() {
    # Domain stats accounting chain cleanup
    for stats_chain in "$DOMAIN_STATS_CHAIN" "$LEGACY_DOMAIN_STATS_CHAIN"; do
        if [ -z "$stats_chain" ]; then continue; fi
        cleanup_domain_stats_jumps "$stats_chain"
        iptables -F "$stats_chain" >/dev/null 2>&1
        iptables -X "$stats_chain" >/dev/null 2>&1
    done

    # Destroy per-domain ipsets (legacy and compact prefixes)
    for set_name in $(ipset list -n 2>/dev/null | grep -E "^(${legacy_domain_prefix}|${compact_domain_prefix})"); do
        ipset destroy "$set_name" >/dev/null 2>&1
    done
}

cleanup_firewall() {
    # Delete all duplicates left by interrupted/partial previous runs.
    cleanup_mangle_mark_rules
    while ip rule del fwmark "$FWMARK" table "$TABLE_NUM" >/dev/null 2>&1; do :; done

    cleanup_forward_accept_rules
    # DNS interception is shared protection, not owned by an individual TUN.
    cleanup_mss_clamp_rules

    while iptables -t nat -D POSTROUTING -o "$VPN_IFACE" -j MASQUERADE >/dev/null 2>&1; do :; done

    cleanup_domain_stats
}

ensure_dnsmasq_conf_dir() {
    dnsmasq_conf_dir=$(dirname "$DNSMASQ_CONFIG_FILE")
    mkdir -p "$dnsmasq_conf_dir"
    if [ ! -f "$DNSMASQ_MAIN_CONFIG" ]; then
        : > "$DNSMASQ_MAIN_CONFIG" 2>/dev/null || return 0
    fi
    if ! grep -F "conf-dir=$dnsmasq_conf_dir" "$DNSMASQ_MAIN_CONFIG" >/dev/null 2>&1; then
        echo "conf-dir=$dnsmasq_conf_dir,user=root" >> "$DNSMASQ_MAIN_CONFIG"
    fi
}

dnsmasq_conf_references_live_ipset() {
    ipset_fragment="$1"
    for set_name in $(awk -F/ '/^ipset=\// {print $NF}' "$ipset_fragment" 2>/dev/null | tr ',' ' '); do
        [ -z "$set_name" ] && continue
        # Persistent protection/accounting sets do not provide a live route.
        # Keeping their retired DNS selectors can override a current tunnel's
        # selector and leave new answers protected but completely unmarked.
        case "$set_name" in
            "$GUARD_IPSET"|vpn_pin_*|vd_*|vpn_d_*) continue ;;
        esac
        if ipset list "$set_name" >/dev/null 2>&1; then
            return 0
        fi
    done
    return 1
}

cleanup_dnsmasq_conf_fragments() {
    dnsmasq_conf_dir=$(dirname "$DNSMASQ_CONFIG_FILE")
    live_domains_file=$(mktemp /tmp/vpn-manager-live-dns.XXXXXX) || return 1
    for live_fragment in "$dnsmasq_conf_dir"/vpn_dns*.conf; do
        [ -f "$live_fragment" ] || continue
        if dnsmasq_conf_references_live_ipset "$live_fragment"; then
            awk -F/ '/^ipset=\// {for (i=2;i<NF;i++) print tolower($i)}' "$live_fragment" >> "$live_domains_file"
        fi
    done
    for fragment in "$dnsmasq_conf_dir"/vpn_dns*.conf "$dnsmasq_conf_dir"/vpn_dns*.conf.bak; do
        [ -e "$fragment" ] || continue
        if [ "$fragment" = "$DNSMASQ_CONFIG_FILE" ] && [ "${1:-}" != "include-current" ]; then
            continue
        fi
        case "$fragment" in
            *.conf.bak) ;;
            *)
                if dnsmasq_conf_references_live_ipset "$fragment"; then
                    continue
                fi
                # Keep learning destinations of stopped routes into the guard,
                # but remove selectors now covered by a live route. dnsmasq's
                # more-specific retired selectors must not shadow live parents.
                pruned_fragment=$(mktemp "$dnsmasq_conf_dir/.vpn-dns-prune.XXXXXX") || { rm -f "$live_domains_file"; return 1; }
                if ! awk -F/ '
                    FILENAME == ARGV[1] {live[$0]=1; next}
                    function covered(domain) {
                        domain=tolower(domain)
                        if (domain in live) return 1
                        while (index(domain,".") > 0) {
                            sub(/^[^.]*\./,"",domain)
                            if (domain in live) return 1
                        }
                        return 0
                    }
                    ($1 == "ipset=" || $1 == "server=") && NF > 2 {
                        selector=$1 "/"; count=0
                        for (i=2;i<NF;i++) if (!covered($i)) {selector=selector $i "/"; count++}
                        if (count) print selector $NF
                        next
                    }
                    {print}
                ' "$live_domains_file" "$fragment" > "$pruned_fragment"; then
                    rm -f "$pruned_fragment" "$live_domains_file"
                    return 1
                fi
                if cmp -s "$fragment" "$pruned_fragment"; then
                    rm -f "$pruned_fragment"
                    continue
                fi
                if [ -s "$pruned_fragment" ]; then
                    mv "$pruned_fragment" "$fragment" || { rm -f "$live_domains_file"; return 1; }
                    DNSMASQ_CONFIG_CHANGED=1
                    continue
                fi
                rm -f "$pruned_fragment"
                ;;
        esac
        if rm -f "$fragment" >/dev/null 2>&1; then
            DNSMASQ_CONFIG_CHANGED=1
        fi
    done
    rm -f "$live_domains_file"
}

render_dnsmasq_config() {
    echo "--> Generating dnsmasq config to populate ipset..."
    ensure_dnsmasq_conf_dir
    cleanup_dnsmasq_conf_fragments || return 1

    tmp_file="${DNSMASQ_CONFIG_FILE}.$$"
    : > "$tmp_file" || return 1

    domain_count=$(count_active_domains)
    ENABLE_DOMAIN_STATS=1
    if ! domain_stats_enabled "$domain_count"; then
        ENABLE_DOMAIN_STATS=0
        echo "--> Domain stats disabled for ${domain_count} domains."
    fi

    # Create per-domain accounting chain only when needed.
    if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
        echo "--> Creating domain stats accounting chain '$DOMAIN_STATS_CHAIN'..."
        iptables -N "$DOMAIN_STATS_CHAIN" 2>/dev/null || true
    fi

    grep -vE '^\s*#|^\s*$' "$DOMAIN_LIST" | while IFS= read -r domain_raw; do
        domain=$(echo "$domain_raw" | tr -d '\r')
        if [ -z "$domain" ]; then continue; fi

        if is_ipv4_entry "$domain"; then
            if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
                # Static IP/CIDR entries are already routed through the shared
                # ipset; direct rules let the traffic table show them by value.
                iptables -A "$DOMAIN_STATS_CHAIN" -d "$domain" -m comment --comment "${domain}|up" >/dev/null 2>&1 || true
                iptables -A "$DOMAIN_STATS_CHAIN" -s "$domain" -m comment --comment "${domain}|dn" >/dev/null 2>&1 || true
            fi
            continue
        fi

        ipset_targets="$GUARD_IPSET${PINNED_IPSETS:+,$PINNED_IPSETS},$IPSET_NAME"
        dom_set=""

        if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
            # Per-domain ipset for traffic accounting.
            # Use a short hash to keep ipset name under 31 chars.
            dom_hash=$(short_hash "${IPSET_NAME}:${domain}")
            dom_set="${compact_domain_prefix}${dom_hash}"
            ipset create "$dom_set" hash:ip family inet timeout "$IPSET_TIMEOUT" -exist 2>/dev/null
            ipset_targets="${ipset_targets},${dom_set}"
        fi

        echo "ipset=/$domain/$ipset_targets" >> "$tmp_file"
        if [ -n "$DNS_PROXY_SERVER" ]; then
            echo "server=/$domain/$DNS_PROXY_SERVER" >> "$tmp_file"
        fi

        if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
            # Accounting rules: upload (LAN->VPN, dst=server) and download (VPN->LAN, src=server).
            iptables -A "$DOMAIN_STATS_CHAIN" -m set --match-set "$dom_set" dst -m comment --comment "${domain}|up" >/dev/null 2>&1 || true
            iptables -A "$DOMAIN_STATS_CHAIN" -m set --match-set "$dom_set" src -m comment --comment "${domain}|dn" >/dev/null 2>&1 || true
        fi
    done

    if [ -f "$DNSMASQ_CONFIG_FILE" ] && command -v cmp >/dev/null 2>&1 && cmp -s "$tmp_file" "$DNSMASQ_CONFIG_FILE"; then
        rm -f "$tmp_file"
        return 0
    fi

    DNSMASQ_CONFIG_CHANGED=1
    if [ -f "$DNSMASQ_CONFIG_FILE" ]; then
        mkdir -p "$(dirname "$DNSMASQ_CONFIG_BACKUP")" >/dev/null 2>&1 || true
        cp "$DNSMASQ_CONFIG_FILE" "$DNSMASQ_CONFIG_BACKUP" >/dev/null 2>&1 || true
    else
        rm -f "$DNSMASQ_CONFIG_BACKUP" >/dev/null 2>&1 || true
    fi
    mv "$tmp_file" "$DNSMASQ_CONFIG_FILE"
    cleanup_dnsmasq_conf_fragments || return 1
}

flush_conntrack_if_requested() {
    if [ "$CONNTRACK_FLUSH_ON_APPLY" != "1" ]; then
        return 0
    fi
    if ! command -v conntrack >/dev/null 2>&1; then
        echo "--> conntrack cleanup requested but conntrack binary is not available."
        return 0
    fi
    echo "--> Flushing conntrack entries for routed destinations..."

    if [ "$FAST_RECOVERY" = "1" ]; then
        # The saved set already contains the destinations that need rerouting;
        # do not block recovery on another DNS lookup for every domain.
        for ip in $(ipset save "$IPSET_NAME" | awk '$1 == "add" {print $3}'); do
            conntrack -D -d "$ip" >/dev/null 2>&1 || true
            conntrack -D -s "$ip" >/dev/null 2>&1 || true
        done
        return 0
    fi

    count=0
    flushed=""
    while IFS= read -r entry_raw || [ -n "$entry_raw" ]; do
        entry=$(echo "$entry_raw" | tr -d '\r')
        case "$entry" in
            ""|\#*) continue ;;
        esac

        if is_ipv4_entry "$entry"; then
            case " $flushed " in
                *" $entry "*) ;;
                *)
                    flushed="$flushed $entry"
                    conntrack -D -d "$entry" >/dev/null 2>&1 || true
                    conntrack -D -s "$entry" >/dev/null 2>&1 || true
                    ;;
            esac
            continue
        fi

        count=$((count + 1))
        if [ "${PRIME_MAX_DOMAINS:-0}" -gt 0 ] && [ "$count" -gt "$PRIME_MAX_DOMAINS" ]; then
            echo "--> Conntrack cleanup capped at ${PRIME_MAX_DOMAINS} domains."
            break
        fi

        for dns_server in 127.0.0.1 $(printf '%s\n' "$DNS_PRIME_SERVERS" | tr ',;' ' '); do
            dns_server=$(echo "$dns_server" | tr -d '\r')
            if [ -z "$dns_server" ]; then
                continue
            fi
            for ip in $(resolve_domain_ips "$entry" "$dns_server"); do
                case " $flushed " in
                    *" $ip "*) ;;
                    *)
                        flushed="$flushed $ip"
                        conntrack -D -d "$ip" >/dev/null 2>&1 || true
                        conntrack -D -s "$ip" >/dev/null 2>&1 || true
                        ;;
                esac
            done
        done
    done < "$DOMAIN_LIST"
}

# Main function to add rules and routes
add_routes() {
    echo "Configuring DNS and policy-based routing..."

    handle_ipv6_mode || exit 1
    enable_ipv4_forwarding

    # Validate the dataplane before removing the previously working rules.
    # If the tunnel disappeared, retain protection and fail closed.
    echo "--> Validating VPN route in table '$TABLE_NUM'..."
    if ! validate_vpn_route_or_safe_direct; then
        echo "Error: failed to create a usable VPN route for '$VPN_IFACE' in table '$TABLE_NUM'." >&2
        exit 1
    fi

    # 1. Cleanup old rules to ensure a fresh start
    cleanup_firewall

    # 2. Create or reset the shared ipset that will store our IPs
    echo "--> Preparing ipset '$IPSET_NAME'..."
    if ! ensure_shared_ipset; then
        echo "Error: failed to prepare ipset '$IPSET_NAME'." >&2
        exit 1
    fi

    # 3. Setup dnsmasq to populate the ipset automatically
    render_dnsmasq_config || exit 1

    if ! apply_dnsmasq_changes; then
        exit 1
    fi

    # 3c. Prime static IP ranges and current DNS answers.
    echo "--> Priming static IP routes..."
    prime_static_entries

    echo "--> Priming ipsets with current DNS answers..."
    prime_ipsets

    # 4. Re-check the policy route before any packets are marked.
    echo "--> Re-validating VPN route in table '$TABLE_NUM'..."
    if ! validate_vpn_route_or_safe_direct; then
        echo "Error: failed to create a usable VPN route for '$VPN_IFACE' in table '$TABLE_NUM'." >&2
        exit 1
    fi

    # 5. Create a rule to mark packets destined for our dynamic ipset
    echo "--> Ensuring iptables mangle rules..."
    if ! ensure_mangle_mark_rules; then
        echo "Error: failed to insert the mangle mark rules." >&2
        exit 1
    fi

    # 6. Add forwarding and NAT rules to allow traffic into the tunnel
    forward_chain=$(resolve_forward_chain)
    echo "--> Ensuring firewall FORWARD and NAT rules..."
    if ! iptables -C "$forward_chain" -i "$LAN_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; then
        if ! iptables -I "$forward_chain" -i "$LAN_IFACE" -o "$VPN_IFACE" -j ACCEPT; then
            echo "Error: failed to insert the FORWARD rule into chain '$forward_chain'." >&2
            exit 1
        fi
    fi
    if iface_exists "$DOCKER_IFACE"; then
        if ! iptables -C FORWARD -i "$DOCKER_IFACE" -o "$VPN_IFACE" -j ACCEPT >/dev/null 2>&1; then
            if ! iptables -I FORWARD -i "$DOCKER_IFACE" -o "$VPN_IFACE" -j ACCEPT; then
                echo "Error: failed to insert the Docker FORWARD rule." >&2
                exit 1
            fi
        fi
    fi
    if [ "$VPN_MASQUERADE" = "1" ]; then
        if ! iptables -t nat -C POSTROUTING -o "$VPN_IFACE" -j MASQUERADE >/dev/null 2>&1; then
            if ! iptables -t nat -I POSTROUTING -o "$VPN_IFACE" -j MASQUERADE; then
                echo "Error: failed to insert the MASQUERADE rule for '$VPN_IFACE'." >&2
                exit 1
            fi
        fi
    fi

    # 6a. Force LAN clients to use router dnsmasq so ipset is populated.
    if ! ensure_dns_hijack_rules; then
        echo "Error: failed to add DNS hijack rules." >&2
        exit 1
    fi

    # 6b. Clamp TCP MSS to avoid MTU blackholes through tunnels.
    if ! ensure_mss_clamp_rules; then
        echo "Warning: failed to add TCPMSS clamp rules; some HTTPS sites may stall on low-MTU tunnels." >&2
    fi

    # 6c. Insert domain stats accounting chain into FORWARD (both directions)
    if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
        echo "--> Ensuring domain stats accounting jump..."
        if ! ensure_domain_stats_jumps; then
            echo "Warning: failed to attach domain stats accounting chain; routing will continue." >&2
        fi
    fi

    # 7. Create a rule to use the new table for marked packets
    echo "--> Ensuring ip rule for marked packets..."
    while ip rule del fwmark "$FWMARK" table "$TABLE_NUM" >/dev/null 2>&1; do :; done
    if ! ip rule add fwmark "$FWMARK" table "$TABLE_NUM"; then
        echo "Error: failed to add ip rule for mark '$FWMARK' and table '$TABLE_NUM'." >&2
        exit 1
    fi

    # 8. Flush routing cache and, optionally, stale conntrack entries.
    ip route flush cache >/dev/null 2>&1 || true
    flush_conntrack_if_requested

    echo "Configuration complete. IP set will now be populated dynamically by DNS queries."
}

sync_routes() {
    echo "Refreshing DNS and policy-based routing..."

    if ! shared_ipset_ready; then
        echo "--> Shared ipset '$IPSET_NAME' is missing or outdated; falling back to full add."
        add_routes
        return
    fi
    if ! base_routing_ready; then
        echo "--> Base routing rules are missing; falling back to full add."
        add_routes
        return
    fi
    if ! validate_vpn_route_or_safe_direct; then
        echo "Error: failed to validate VPN route for sync." >&2
        exit 1
    fi

    cleanup_domain_stats
    cleanup_mss_clamp_rules
    if [ "$IPSET_FLUSH_ON_SYNC" = "1" ]; then
        ipset flush "$IPSET_NAME" >/dev/null 2>&1
    else
        echo "--> Soft sync: keeping existing ipset entries; stale dynamic entries will expire by timeout."
    fi

    render_dnsmasq_config || exit 1

    if ! apply_dnsmasq_changes; then
        exit 1
    fi

    echo "--> Priming static IP routes..."
    prime_static_entries

    echo "--> Priming ipsets with current DNS answers..."
    prime_ipsets

    if [ "$ENABLE_DOMAIN_STATS" = "1" ]; then
        echo "--> Restoring domain stats accounting jump..."
        if ! ensure_domain_stats_jumps; then
            echo "Warning: failed to attach domain stats accounting chain; routing will continue." >&2
        fi
    fi

    if ! ensure_dns_hijack_rules; then
        echo "Error: failed to refresh DNS hijack rules." >&2
        exit 1
    fi
    if ! ensure_mss_clamp_rules; then
        echo "Warning: failed to refresh TCPMSS clamp rules." >&2
    fi

    ip route flush cache >/dev/null 2>&1 || true
    flush_conntrack_if_requested
    echo "Domain routing refreshed."
}

# Main function to delete all rules
delete_routes() {
    echo "Deleting all routing rules and firewall marks..."

    # 1. Cleanup firewall and routing rules
    cleanup_firewall

    # 2. Flush the custom routing table
    echo "--> Flushing route table '$TABLE_NUM'..."
    ip route flush table "$TABLE_NUM" >/dev/null 2>&1

    # 3. Destroy the ipset
    echo "--> Destroying ipset '$IPSET_NAME'..."
    ipset destroy "$IPSET_NAME" >/dev/null 2>&1

    # 4. Remove dnsmasq config and reload only if it existed
    if [ -f "$DNSMASQ_CONFIG_FILE" ]; then
        echo "--> Removing dnsmasq config and reloading..."
        cp "$DNSMASQ_CONFIG_FILE" "$DNSMASQ_CONFIG_BACKUP" >/dev/null 2>&1 || true
        # Keep learning protected destinations while the runtime is stopped.
        # Remove references to the route's deleted sets, retaining the guard.
        sed "s|^ipset=\\(/[^ ]*/\\).*|ipset=\\1${GUARD_IPSET}${PINNED_IPSETS:+,$PINNED_IPSETS}|" "$DNSMASQ_CONFIG_FILE" > "${DNSMASQ_CONFIG_FILE}.tmp" || exit 1
        mv "${DNSMASQ_CONFIG_FILE}.tmp" "$DNSMASQ_CONFIG_FILE" || exit 1
        DNSMASQ_CONFIG_CHANGED=1
        # Do not leave a retired guard-only selector shadowing the DNS entry
        # installed by the replacement tunnel. Historical IP guards stay intact.
        cleanup_dnsmasq_conf_fragments include-current || exit 1
        if ! apply_dnsmasq_changes; then
            exit 1
        fi
    else
        echo "--> dnsmasq config already absent; reload skipped."
    fi

    # 5. Flush routing cache
    ip route flush cache >/dev/null 2>&1 || true

    echo "Cleanup complete."
}

# --- SCRIPT LOGIC ---
# Check for required utilities
for cmd in ip iptables ipset tr grep awk sed; do
    if ! command -v "$cmd" >/dev/null 2>&1; then
        echo "Error: required utility '$cmd' not found."
        exit 1
    fi
done

setup_iptables_wait
if ! acquire_routing_lock; then
    exit 1
fi
trap release_routing_lock EXIT INT TERM

case "$1" in
    guard)
        protect_known_destinations || exit 1
        ;;
    add)
        protect_known_destinations || exit 1
        add_routes
        protect_known_destinations || exit 1
        ;;
    sync|refresh)
        protect_known_destinations || exit 1
        sync_routes
        protect_known_destinations || exit 1
        ;;
    del|delete)
        protect_known_destinations || exit 1
        delete_routes
        ;;
    *)
        echo "Usage: $0 {add|sync|del}"
        exit 1
        ;;
esac

exit 0
