require_configured
json=$(pgbr_info_json)
[[ -n $json ]] || die "pgbackrest info failed — run: postgres-manager check"
header "Backups · stanza $STANZA · $(repo_label)"
if [[ $(jq -r '.[0].status.code' <<<"$json") != 0 ]]; then
  warn "$(jq -r '.[0].status.message' <<<"$json")"
  exit 0
fi

printf '  %s%-34s %-5s %-17s %9s %10s %10s%s\n' "$D" LABEL TYPE STARTED DURATION SIZE REPO "$N"
while IFS=$'\t' read -r label type start stop size delta; do
  printf '  %-34s %-5s %-17s %9s %10s %10s\n' "$label" "$type" "$(fmt_ts "$start")" \
    "$(duration $((stop - start)))" "$(hb "$size")" "$(hb "$delta")"
done < <(jq -r '.[0].backup[] | [.label, .type, .timestamp.start, .timestamp.stop, .info.size, .info.repository.delta] | @tsv' <<<"$json")

IFS=$'\t' read -r oldest wal_min wal_max total < <(jq -r '.[0] | [.backup[0].timestamp.stop, (.archive[-1].min // "-"),
  (.archive[-1].max // "-"), ([.backup[].info.repository.delta] | add)] | @tsv' <<<"$json")
echo
row PITR "any point from $(fmt_ts "$oldest") until the last archived WAL"
row WAL "$wal_min … $wal_max"
row Repo "$(hb "$total") (backups, excl. WAL)"
