# NAT64: Lightweight NAT64 Server for Linux

NAT64 is a lightweight NAT64 server designed for Linux systems, enabling the forwarding of IPv6 TCP traffic to IPv4.

## **Table of Contents**

- [Build](#build)
- [Setup](#setup)
- [Usage](#usage)
- [TCP-Brutal](#tcp-brutal)
- [Run as Service](#run-as-service)
- [License](#license)
- [Contributing](#contributing)

## **Build**

To compile the NAT64 server, use the following command:

```bash
GOARCH=amd64 GOOS=linux go build -gcflags="-N -l" -ldflags="-s -w" -a
```

This command sets the architecture to `amd64` and the operating system to `linux`, optimizing the build by stripping debugging information and reducing the binary size.

## **Setup**

To set up the NAT64 server, follow these steps:

1. Configure your localhost to bind to a `/96` IPv6 prefix. This is essential for the NAT64 server to function correctly.
2. Forward the `/96` subnet traffic to `[::1]:port`, where the default port is `8888`. This ensures that incoming IPv6 traffic is directed to the NAT64 server.

   ```bash
   ip6tables -t mangle -A PREROUTING -d [your_prefix]/96 -p tcp -j TPROXY --on-port=8888 --on-ip=::1
   ```

3. Run the server by executing the following command:

   ```bash
   ./nat64
   ```

## **Usage**

The NAT64 server relays IPv6 connections to IPv4 seamlessly. You can customize its behavior using the following command-line options:

```
Usage:
    -p, --port     Port to listen on. Default port is 8888.
    -w, --window   TCP window size. Requires the tcp-brutal kernel module.
    -h, --help     Show this help message.
```

- **-p, --port:** Specify the port for the server to listen on. The default is `8888`.
- **-w, --window:** Set the TCP window size. Use this option if the default congestion control does not perform adequately, particularly in restrictive environments.
- **-h, --help:** Display the help message.

## **TCP-Brutal**

The [TCP Brutal](https://github.com/apernet/tcp-brutal) kernel module allows for the customization of the congestion control algorithm. This feature is particularly useful if the default congestion control does not perform well in certain network conditions.

To enable TCP Brutal support, pass the `-w` parameter when running the NAT64 server.

## **Run as Service**

You can run NAT64 as a service using either `systemd` or `OpenRC`. Below are instructions for both:

### **Systemd Instructions**

1. Create a service file at `/etc/systemd/system/nat64.service` with the following content:

   ```ini
   [Unit]
   Description=NAT64 Server
   After=network.target

   [Service]
   ExecStart=/path/to/nat64 -p 8888
   Restart=always

   [Install]
   WantedBy=multi-user.target
   ```

2. Enable and start the service:

   ```bash
   sudo systemctl enable nat64
   sudo systemctl start nat64
   ```

### **OpenRC (Alpine) Instructions**

1. Create a service script in `/etc/init.d/nat64` with the following content:

   ```bash
   #!/sbin/openrc-run

   command=/path/to/nat64
   command_args="-p 8888"
   pidfile=/run/nat64.pid

   depend() {
       need net
       after network
   }

   start() {
       ebegin "Starting NAT64"
       start-stop-daemon --start --make-pidfile --pidfile ${pidfile} --background --exec ${command} -- ${command_args}
       eend $?
   }
   ```

2. Make the script executable and add it to the default runlevel:

   ```bash
   chmod +x /etc/init.d/nat64
   rc-update add nat64 default
   service nat64 start
   ```

## **License**

This project is licensed under the MIT License. See the LICENSE file for more details.

## **Contributing**

For further inquiries or contributions, please feel free to reach out! Your contributions are welcome to enhance the functionality and performance of the NAT64 server.
