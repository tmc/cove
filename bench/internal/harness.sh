#!/bin/sh

bench_timestamp() {
	date -u +"%Y-%m-%dT%H:%M:%SZ"
}

bench_date() {
	date -u +"%Y%m%d"
}

bench_hostid() {
	hostname -s 2>/dev/null | tr -cd '[:alnum:]-' | tr '[:upper:]' '[:lower:]'
}

bench_json_escape() {
	awk 'BEGIN {
		s = ARGV[1]
		ARGV[1] = ""
		gsub(/\\/,"\\\\",s)
		gsub(/"/,"\\\"",s)
		gsub(/\t/,"\\t",s)
		gsub(/\r/,"\\r",s)
		gsub(/\n/,"\\n",s)
		printf "%s", s
	}' "$1"
}

bench_git_value() {
	git rev-parse "$1" 2>/dev/null || printf "unknown"
}

bench_cove_version() {
	cove_bin=$1
	"$cove_bin" version 2>/dev/null | head -1 || printf "unknown"
}

bench_macos_version() {
	if command -v sw_vers >/dev/null 2>&1; then
		printf "%s %s" "$(sw_vers -productName 2>/dev/null)" "$(sw_vers -productVersion 2>/dev/null)"
	else
		uname -s
	fi
}

bench_cpu_model() {
	sysctl -n machdep.cpu.brand_string 2>/dev/null || uname -m
}

bench_mem_bytes() {
	sysctl -n hw.memsize 2>/dev/null || printf "0"
}

bench_disk_free_bytes() {
	root=${1:-$HOME/.vz}
	mkdir -p "$root" 2>/dev/null || true
	df -k "$root" 2>/dev/null | awk 'NR==2 {print $4 * 1024}' || printf "0"
}

bench_write_host_json() {
	out=$1
	cove_bin=${2:-./cove}
	mkdir -p "$(dirname "$out")"
	cat >"$out" <<EOF
{
  "timestamp": "$(bench_timestamp)",
  "hostname": "$(bench_json_escape "$(hostname 2>/dev/null || printf unknown)")",
  "hostid": "$(bench_json_escape "$(bench_hostid)")",
  "git_head": "$(bench_json_escape "$(bench_git_value HEAD)")",
  "origin_main": "$(bench_json_escape "$(bench_git_value origin/main)")",
  "cove_version": "$(bench_json_escape "$(bench_cove_version "$cove_bin")")",
  "macos": "$(bench_json_escape "$(bench_macos_version)")",
  "kernel": "$(bench_json_escape "$(uname -a)")",
  "cpu": "$(bench_json_escape "$(bench_cpu_model)")",
  "cpu_count": $(sysctl -n hw.ncpu 2>/dev/null || printf 0),
  "memory_bytes": $(bench_mem_bytes),
  "vm_root": "$(bench_json_escape "$HOME/.vz/vms")",
  "image_root": "$(bench_json_escape "$HOME/.vz/images")",
  "runs_root": "$(bench_json_escape "$HOME/.vz/runs")",
  "disk_free_bytes": $(bench_disk_free_bytes "$HOME/.vz")
}
EOF
}

bench_default_out_dir() {
	name=$1
	printf "bench/%s/results-%s-%s" "$name" "$(bench_date)" "$(bench_hostid)"
}

bench_emit_skip() {
	jsonl=$1
	benchmark=$2
	reason=$3
	printf '{"timestamp":"%s","benchmark":"%s","status":"skip","reason":"%s"}\n' \
		"$(bench_timestamp)" \
		"$(bench_json_escape "$benchmark")" \
		"$(bench_json_escape "$reason")" >>"$jsonl"
}

bench_emit_not_measured() {
	jsonl=$1
	benchmark=$2
	tool=$3
	reason=$4
	printf '{"timestamp":"%s","benchmark":"%s","tool":"%s","status":"not_measured","reason":"%s"}\n' \
		"$(bench_timestamp)" \
		"$(bench_json_escape "$benchmark")" \
		"$(bench_json_escape "$tool")" \
		"$(bench_json_escape "$reason")" >>"$jsonl"
}

bench_ms_now() {
	perl -MTime::HiRes=time -e 'printf "%d\n", time() * 1000'
}

bench_summary_header() {
	md=$1
	title=$2
	jsonl=$3
	host_json=$4
	commit=${5:-$(bench_git_value HEAD)}
	cat >"$md" <<EOF
# $title

- Date: $(bench_timestamp)
- Cove commit: \`$commit\`
- Host metadata: \`$host_json\`
- Raw results: \`$jsonl\`

EOF
}

bench_append_duration_stats() {
	md=$1
	jsonl=$2
	key=$3
	label=$4
	perl -MJSON::PP -e '
		my ($file, $key, $label) = @ARGV;
		open my $fh, "<", $file or die "$file: $!";
		my @v;
		while (my $line = <$fh>) {
			my $row = eval { decode_json($line) };
			next if !$row || !exists $row->{$key} || $row->{$key} !~ /^\d+$/;
			push @v, 0 + $row->{$key};
		}
		exit 0 if !@v;
		@v = sort { $a <=> $b } @v;
		my $n = @v;
		my $sum = 0; $sum += $_ for @v;
		my $pct = sub {
			my $p = shift;
			my $idx = int(($p * ($n - 1)) + 0.999999);
			return $v[$idx];
		};
		my $median = $v[int($n / 2)];
		if ($n % 2 == 0) {
			$median = int(($v[$n / 2 - 1] + $v[$n / 2]) / 2);
		}
		printf "\n## %s summary\n\n", $label;
		printf "| n | mean | median | p95 | p99 |\n";
		printf "|---:|---:|---:|---:|---:|\n";
		printf "| %d | %dms | %dms | %dms | %dms |\n",
			$n, int($sum / $n), $median, $pct->(0.95), $pct->(0.99);
	' "$jsonl" "$key" "$label" >>"$md"
}
