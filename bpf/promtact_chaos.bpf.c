// SPDX-License-Identifier: GPL-2.0
#include <linux/bpf.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/pkt_cls.h>
#include <bpf/bpf_endian.h>
#include <bpf/bpf_helpers.h>

struct fault_policy {
    __u32 xdp_drop_per_10000;
    __u32 tc_drop_per_10000;
    __u32 tc_corrupt_per_10000;
    __u32 blocked_src;
    __u32 blocked_dst;
};

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 1);
    __type(key, __u32);
    __type(value, struct fault_policy);
} policy SEC(".maps");

static __always_inline int parse_ipv4(void *data, void *end,
                                      struct iphdr **ip) {
    struct ethhdr *eth = data;
    if ((void *)(eth + 1) > end || eth->h_proto != bpf_htons(ETH_P_IP))
        return 0;
    *ip = (void *)(eth + 1);
    return (void *)(*ip + 1) <= end;
}

SEC("xdp")
int promtact_xdp(struct xdp_md *ctx) {
    void *data = (void *)(long)ctx->data;
    void *end = (void *)(long)ctx->data_end;
    struct iphdr *ip;
    __u32 zero = 0;
    struct fault_policy *p = bpf_map_lookup_elem(&policy, &zero);
    if (!p || !parse_ipv4(data, end, &ip))
        return XDP_PASS;
    if ((p->blocked_src && ip->saddr == p->blocked_src) ||
        (p->blocked_dst && ip->daddr == p->blocked_dst))
        return XDP_DROP;
    if (p->xdp_drop_per_10000 &&
        bpf_get_prandom_u32() % 10000 < p->xdp_drop_per_10000)
        return XDP_DROP;
    return XDP_PASS;
}

SEC("classifier")
int promtact_tc(struct __sk_buff *skb) {
    __u32 zero = 0;
    struct fault_policy *p = bpf_map_lookup_elem(&policy, &zero);
    if (!p)
        return TC_ACT_OK;
    if (p->tc_drop_per_10000 &&
        bpf_get_prandom_u32() % 10000 < p->tc_drop_per_10000)
        return TC_ACT_SHOT;
    if (p->tc_corrupt_per_10000 &&
        bpf_get_prandom_u32() % 10000 < p->tc_corrupt_per_10000) {
        __u8 value = 0x01;
        bpf_skb_store_bytes(skb, ETH_HLEN + sizeof(struct iphdr), &value,
                            sizeof(value), BPF_F_RECOMPUTE_CSUM);
    }
    return TC_ACT_OK;
}

char LICENSE[] SEC("license") = "GPL";

