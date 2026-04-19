#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/ioctl.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>
#include <net/if.h>
#include <errno.h>
#include <ifaddrs.h>

#define INTERFACE "usb0"
#define IP_ADDR   "10.10.10.1"
#define NETMASK   "255.255.255.0"

int main() {
    int sockfd;
    struct ifreq ifr;
    struct sockaddr_in *sin;

    // Create a socket for network configuration
    sockfd = socket(AF_INET, SOCK_DGRAM, 0);
    if (sockfd < 0) {
        perror("Socket creation failed");
        return EXIT_FAILURE;
    }

    memset(&ifr, 0, sizeof(ifr));
    strncpy(ifr.ifr_name, INTERFACE, IFNAMSIZ - 1);

    // Check if interface exists (equivalent to [ -e /sys/class/net/usb0 ])
    if (ioctl(sockfd, SIOCGIFFLAGS, &ifr) < 0) {
        fprintf(stderr, "Error: Interface %s not found.\n", INTERFACE);
        
        struct ifaddrs *ifaddr, *ifa;
        if (getifaddrs(&ifaddr) == -1) {
            perror("getifaddrs");
        } else {
            printf("Currently available interfaces:\n");
            for (ifa = ifaddr; ifa != NULL; ifa = ifa->ifa_next) {
                if (ifa->ifa_addr == NULL) continue;
                
                // Only print each interface name once (getifaddrs returns 
                // separate entries for IPv4 and IPv6)
                if (ifa->ifa_addr->sa_family == AF_PACKET) {
                    printf("  - %s\n", ifa->ifa_name);
                }
            }
            freeifaddrs(ifaddr);
        }

        close(sockfd);
        return EXIT_FAILURE;
    }

    // Configure IP Address
    sin = (struct sockaddr_in *)&ifr.ifr_addr;
    sin->sin_family = AF_INET;
    if (inet_pton(AF_INET, IP_ADDR, &sin->sin_addr) <= 0) {
        fprintf(stderr, "Invalid IP address format: %s\n", IP_ADDR);
        close(sockfd);
        return EXIT_FAILURE;
    }

    if (ioctl(sockfd, SIOCSIFADDR, &ifr) < 0) {
        perror("Failed to set IP address");
        close(sockfd);
        return EXIT_FAILURE;
    }

    // Configure Netmask (/24)
    if (inet_pton(AF_INET, NETMASK, &sin->sin_addr) <= 0) {
        close(sockfd);
        return EXIT_FAILURE;
    }
    if (ioctl(sockfd, SIOCSIFNETMASK, &ifr) < 0) {
        perror("Failed to set netmask");
        close(sockfd);
        return EXIT_FAILURE;
    }

    // Bring Interface UP (equivalent to ip link set usb0 up)
    if (ioctl(sockfd, SIOCGIFFLAGS, &ifr) < 0) {
        perror("Failed to get flags");
        close(sockfd);
        return EXIT_FAILURE;
    }

    ifr.ifr_flags |= (IFF_UP | IFF_RUNNING);
    if (ioctl(sockfd, SIOCSIFFLAGS, &ifr) < 0) {
        perror("Failed to set interface UP");
        close(sockfd);
        return EXIT_FAILURE;
    }

    printf("Dev network interface %s is up and configured with %s\n", INTERFACE, IP_ADDR);

    close(sockfd);
    return EXIT_SUCCESS;
}