// Licensed under the Apache License, Version 2.0.

use promtact_rust_tokenring_adapter::adapter::Adapter;
use promtact_rust_tokenring_adapter::protocol::{
    HelloRequest, Message, Operation, Request, Response, VERSION,
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

fn hello(id: u64) -> Request {
    let mut request = request(id, Operation::Hello);
    request.hello = Some(HelloRequest { seed: 17 });
    request
}

fn assert_error(response: Response, code: &str) {
    let error = response.error.expect("response must contain error");
    assert_eq!(error.code, code);
    assert!(response.hello.is_none());
    assert!(response.messages.is_empty());
    assert!(response.violation.is_none());
}

#[test]
fn adapter_rejects_invalid_envelopes_and_ordering() {
    let mut adapter = Adapter::new(3).expect("create adapter");

    let mut unsupported = hello(1);
    unsupported.version = VERSION + 1;
    let response = adapter.handle(unsupported);
    assert_eq!(response.version, VERSION);
    assert_eq!(response.id, 1);
    assert_eq!(response.op, Operation::Hello);
    assert_error(response, "unsupported-version");

    let response = adapter.handle(hello(0));
    assert_error(response, "invalid-request");

    let response = adapter.handle(request(2, Operation::Hello));
    assert_error(response, "invalid-request");

    let mut tick = request(3, Operation::Tick);
    tick.node = Some(1);
    let response = adapter.handle(tick);
    assert_error(response, "not-initialized");

    let response = adapter.handle(hello(4));
    assert!(response.error.is_none());

    let response = adapter.handle(hello(5));
    assert_error(response, "already-initialized");

    let mut malformed_check = request(6, Operation::Check);
    malformed_check.node = Some(1);
    let response = adapter.handle(malformed_check);
    assert_error(response, "invalid-request");
}

#[test]
fn adapter_rejects_unknown_nodes() {
    let mut adapter = Adapter::new(3).expect("create adapter");
    assert!(adapter.handle(hello(1)).error.is_none());

    for (id, op) in [(2, Operation::Tick), (3, Operation::Drain)] {
        let mut request = request(id, op);
        request.node = Some(99);

        let response = adapter.handle(request);
        assert_error(response, "unknown-node");
    }

    let mut request = request(4, Operation::Deliver);
    request.node = Some(99);
    request.message = Some(valid_message());

    let response = adapter.handle(request);
    assert_error(response, "unknown-node");
}

#[test]
fn adapter_rejects_invalid_token_messages() {
    let mut adapter = Adapter::new(3).expect("create adapter");
    assert!(adapter.handle(hello(1)).error.is_none());

    let mut wrong_kind = valid_message();
    wrong_kind.kind = 2;
    assert_invalid_message(&mut adapter, 2, wrong_kind);

    let mut missing_payload = valid_message();
    missing_payload.payload = None;
    assert_invalid_message(&mut adapter, 3, missing_payload);

    let mut short_payload = valid_message();
    short_payload.payload = Some(vec![1, 2, 3]);
    assert_invalid_message(&mut adapter, 4, short_payload);

    let mut wrong_digest = valid_message();
    wrong_digest.value ^= 1;
    assert_invalid_message(&mut adapter, 5, wrong_digest);
}

fn valid_message() -> Message {
    let sequence = 7_u64;

    Message {
        from: 1,
        to: 2,
        kind: 1,
        value: (sequence << 32) | (1_u64 << 16) | 2,
        payload: Some(sequence.to_be_bytes().to_vec()),
    }
}

fn assert_invalid_message(adapter: &mut Adapter, id: u64, message: Message) {
    let mut request = request(id, Operation::Deliver);
    request.node = Some(2);
    request.message = Some(message);

    let response = adapter.handle(request);
    assert_error(response, "invalid-message");
}
