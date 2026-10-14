# Flash the canteen node: stamps ROLE + current laptop LAN IP, uploads, restores.
# Usage: just a | just b | just kitchen   [PORT=/dev/cu.usbmodemXXX]
port := "/dev/cu.usbmodem101"
fqbn := "esp32:esp32:esp32c3"
ino  := "canteen-node/canteen-node.ino"

a: (flash "ROLE_COUNTER_A")
b: (flash "ROLE_COUNTER_B")
kitchen: (flash "ROLE_KITCHEN")

[private]
flash role:
    #!/bin/zsh
    set -e
    ip=$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1)
    echo "broker=$ip role={{role}} port={{port}}"
    cp {{ino}} /tmp/canteen-node.ino.bak
    trap 'mv /tmp/canteen-node.ino.bak {{ino}}' EXIT
    sed -i '' 's/^#define ROLE .*/#define ROLE {{role}}/' {{ino}}
    sed -i '' 's/^#define MQTT_HOST ".*"/#define MQTT_HOST "'"$ip"'"/' {{ino}}
    grep -E '^#define (ROLE|MQTT_HOST)' {{ino}}
    for pid in $(lsof -t {{port}} 2>/dev/null); do kill $pid 2>/dev/null; done
    sleep 1
    arduino-cli compile --fqbn {{fqbn}} --upload -p {{port}} canteen-node
