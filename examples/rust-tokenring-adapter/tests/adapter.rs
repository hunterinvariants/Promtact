// Licensed under the Apache License, Version 2.0.

use promtact_rust_tokenring_adapter::adapter::Adapter;
use promtact_rust_tokenring_adapter::protocol::{
    HelloRequest, Message, Operation, Request, VERSION,
};

fn request(id: u64, op: Operation) -> Request {
    Request {
        version: VERSION,
        id,
        op,
        hello: None,
        node: None,
        message: None,
    }
}

fn hello(id: u64, seed: i64) -> Request {
    let mut request = request(id, Operation::Hello);
    request.hello = Some(HelloRequest { seed });
    request
}

fn node_request(id: u64, op: Operation, node: u32) -> Request {
    let mut request = request(id, op);
    request.node = Some(node);
    request
}

fn deliver(id: u64, node: u32, message: Message) -> Request {
    let mut request = node_request(id, Operation::Deliver, node);
    request.message = Some(message);
    request
}

fn digest(sequence: u64, from: u32, to: u32) -> u64 {
    (sequence << 32) | (u64::from(from) << 16) | u64::from(to)
}

#[test]
fn adapter_drives_token_ring_lifecycle() {
    let mut adapter = Adapter::new(3).expect("create adapter");

    let response = adapter.handle(hello(1, 0x4a2c));
    assert!(response.error.is_none());
    assert_eq!(response.hello.expect("hello response").nodes, vec![1, 2, 3]);

    let response = adapter.handle(node_request(2, Operation::Tick, 1));
    assert!(response.error.is_none());

    let response = adapter.handle(node_request(3, Operation::Drain, 1));
    assert!(response.error.is_none());
    assert_eq!(response.messages.len(), 1);

    let message = response.messages[0].clone();
    assert_eq!(message.from, 1);
    assert_eq!(message.to, 2);
    assert_eq!(message.kind, 1);
    assert_eq!(message.value, digest(1, 1, 2));
    assert_eq!(
        message.payload.as_deref(),
        Some(1_u64.to_be_bytes().as_slice())
    );

    let response = adapter.handle(deliver(4, 2, message));
    assert!(response.error.is_none());

    let response = adapter.handle(request(5, Operation::Check));
    assert!(response.error.is_none());
    assert!(response.violation.is_none());

    assert_eq!(adapter.holder_ids(), vec![2]);
    assert_eq!(adapter.passes(), 1);

    let response = adapter.handle(request(6, Operation::Close));
    assert!(response.error.is_none());

    let response = adapter.handle(request(7, Operation::Check));
    let error = response.error.expect("operation after close must fail");
    assert_eq!(error.code, "adapter-closed");
}

#[test]
fn check_reports_deliberate_duplicate_token() {
    let mut adapter = Adapter::new(3).expect("create adapter");

    assert!(adapter.handle(hello(1, 0x5eed)).error.is_none());

    let sequence = (1_u64 << 40) | 9;
    let duplicate = Message {
        from: 1,
        to: 2,
        kind: 1,
        value: digest(sequence, 1, 2),
        payload: Some(sequence.to_be_bytes().to_vec()),
    };

    let response = adapter.handle(deliver(2, 2, duplicate));
    assert!(response.error.is_none());

    let response = adapter.handle(request(3, Operation::Check));
    assert!(response.error.is_none());

    let violation = response
        .violation
        .expect("duplicate token must violate invariant");
    assert_eq!(violation.invariant, "at-most-one-token");
    assert_eq!(violation.detail, "token held by nodes [1, 2]");

    adapter.handle(node_request(4, Operation::Tick, 2));
    let response = adapter.handle(node_request(5, Operation::Drain, 2));

    let forwarded = response
        .messages
        .into_iter()
        .next()
        .expect("node two forwards token");

    let next_sequence = sequence + 1;
    assert_eq!(
        forwarded.payload.as_deref(),
        Some(next_sequence.to_be_bytes().as_slice())
    );
    assert_eq!(forwarded.value, digest(next_sequence, 2, 3));
}
