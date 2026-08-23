// Licensed under the Apache License, Version 2.0.

use promtact_rust_tokenring_adapter::protocol::{
    HelloRequest, HelloResponse, Message, Operation, RemoteError, Request, Response, VERSION,
};

fn empty_request(id: u64, op: Operation) -> Request {
    Request {
        version: VERSION,
        id,
        op,
        hello: None,
        node: None,
        message: None,
    }
}

fn empty_response(id: u64, op: Operation) -> Response {
    Response {
        version: VERSION,
        id,
        op,
        error: None,
        hello: None,
        messages: Vec::new(),
        violation: None,
    }
}

#[test]
fn operations_use_protocol_v1_wire_names() {
    let cases = [
        (Operation::Hello, "\"hello\""),
        (Operation::Tick, "\"tick\""),
        (Operation::Drain, "\"drain\""),
        (Operation::Deliver, "\"deliver\""),
        (Operation::Check, "\"check\""),
        (Operation::Close, "\"close\""),
    ];

    for (operation, expected) in cases {
        let encoded = serde_json::to_string(&operation).expect("serialize operation");
        assert_eq!(encoded, expected);

        let decoded: Operation = serde_json::from_str(expected).expect("deserialize operation");
        assert_eq!(decoded, operation);
    }
}

#[test]
fn hello_request_matches_go_json() {
    let mut request = empty_request(1, Operation::Hello);
    request.hello = Some(HelloRequest { seed: -17 });

    let encoded = serde_json::to_string(&request).expect("serialize hello request");

    assert_eq!(
        encoded,
        r#"{"version":1,"id":1,"op":"hello","hello":{"seed":-17}}"#
    );

    let decoded: Request = serde_json::from_str(&encoded).expect("deserialize hello request");
    assert_eq!(decoded, request);
}

#[test]
fn deliver_request_encodes_go_byte_slice_as_base64() {
    let mut request = empty_request(4, Operation::Deliver);
    request.node = Some(2);
    request.message = Some(Message {
        from: 1,
        to: 2,
        kind: 7,
        value: 99,
        payload: Some(b"token".to_vec()),
    });

    let encoded = serde_json::to_string(&request).expect("serialize deliver request");

    assert_eq!(
        encoded,
        concat!(
            r#"{"version":1,"id":4,"op":"deliver","node":2,"#,
            r#""message":{"from":1,"to":2,"kind":7,"value":99,"#,
            r#""payload":"dG9rZW4="}}"#
        )
    );

    let decoded: Request = serde_json::from_str(&encoded).expect("deserialize deliver request");
    assert_eq!(decoded, request);
}

#[test]
fn empty_optional_values_match_go_omitempty() {
    let mut request = empty_request(2, Operation::Tick);
    request.node = Some(1);

    assert_eq!(
        serde_json::to_string(&request).expect("serialize tick request"),
        r#"{"version":1,"id":2,"op":"tick","node":1}"#
    );

    let message = Message {
        from: 1,
        to: 2,
        kind: 3,
        value: 5,
        payload: Some(Vec::new()),
    };

    assert_eq!(
        serde_json::to_string(&message).expect("serialize empty payload"),
        r#"{"from":1,"to":2,"kind":3,"value":5}"#
    );
}

#[test]
fn hello_and_drain_responses_match_go_json() {
    let mut hello = empty_response(1, Operation::Hello);
    hello.hello = Some(HelloResponse {
        nodes: vec![1, 2],
        invariants: vec!["at-most-one-token".to_owned()],
    });

    assert_eq!(
        serde_json::to_string(&hello).expect("serialize hello response"),
        concat!(
            r#"{"version":1,"id":1,"op":"hello","hello":{"nodes":[1,2],"#,
            r#""invariants":["at-most-one-token"]}}"#
        )
    );

    let mut drain = empty_response(3, Operation::Drain);
    drain.messages.push(Message {
        from: 1,
        to: 2,
        kind: 7,
        value: 99,
        payload: Some(b"token".to_vec()),
    });

    let encoded = serde_json::to_string(&drain).expect("serialize drain response");

    assert_eq!(
        encoded,
        concat!(
            r#"{"version":1,"id":3,"op":"drain","messages":[{"from":1,"#,
            r#""to":2,"kind":7,"value":99,"payload":"dG9rZW4="}]}"#
        )
    );

    let decoded: Response = serde_json::from_str(&encoded).expect("deserialize drain response");
    assert_eq!(decoded, drain);
}

#[test]
fn remote_error_matches_go_json() {
    let mut response = empty_response(5, Operation::Check);
    response.error = Some(RemoteError {
        code: "check-failed".to_owned(),
        message: "protocol state unavailable".to_owned(),
    });

    assert_eq!(
        serde_json::to_string(&response).expect("serialize remote error"),
        concat!(
            r#"{"version":1,"id":5,"op":"check","error":{"code":"check-failed","#,
            r#""message":"protocol state unavailable"}}"#
        )
    );
}

#[test]
fn invalid_operation_and_payload_are_rejected() {
    let unknown_operation = r#"{"version":1,"id":1,"op":"unknown","node":1}"#;

    assert!(serde_json::from_str::<Request>(unknown_operation).is_err());

    let invalid_payload = concat!(
        r#"{"version":1,"id":4,"op":"deliver","node":2,"#,
        r#""message":{"from":1,"to":2,"kind":7,"value":99,"#,
        r#""payload":"%%%invalid%%%"}}"#
    );

    assert!(serde_json::from_str::<Request>(invalid_payload).is_err());
}
