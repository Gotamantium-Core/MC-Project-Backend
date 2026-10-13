#!/bin/sh
# Queue simulator for sketch testing: publishes each lane's head, waits for
# the button ack, then advances. Usage: sh mqtt/smoke.sh [broker-host]
H=${1:-127.0.0.1}

run_queue() { # display-topic ack-topic payload... (last payload = empty state)
  t=$1; a=$2; shift 2
  for msg in "$@"; do
    id=${msg%% *}
    mosquitto_pub -h "$H" -t "$t" -m "$msg" -r -q 1
    case "$id" in IDLE|NONE) echo "[$t] drained" ;;
    *)
      echo "[$t] showing: $msg -- press button"
      got=$(mosquitto_sub -h "$H" -t "$a" -C 1)
      [ "$got" = "$id" ] && echo "[$t] acked $got" || echo "[$t] WARNING: expected $id, got $got"
      ;;
    esac
  done
}

run_queue canteen/counter-a/now canteen/counter-a/served \
  "105 Q1" "106 Q0" "IDLE Q0" &
run_queue canteen/kitchen/now canteen/kitchen/done \
  "106 POR Q1" "107 CHK Q0" "IDLE Q0" &
run_queue canteen/counter-b/ready canteen/counter-b/collected \
  "106 POR W4" "107 CHP W3" "108 PUR W2" "109 DIH W1" "110 IDP W0" "NONE W0" &
wait
echo "all queues drained"
