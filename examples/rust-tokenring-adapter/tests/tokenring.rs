// Licensed under the Apache License, Version 2.0.

use promtact_rust_tokenring_adapter::tokenring::{Ring, RingError, TokenMessage};

#[test]
fn new_rejects_too_few_nodes() {
    assert_eq!(
        Ring::new(1).expect_err("one node must fail"),
        RingError::TooFewNodes
    );
}

#[test]
fn token_passes_in_ring_order() {
    let mut ring = Ring::new(3).expect("create three-node ring");

    assert_eq!(ring.holder_ids(), vec![1]);

    ring.advance(1);
    assert!(ring.holder_ids().is_empty());

    let first = ring.take_outbound(1);
    assert_eq!(
        first,
        vec![TokenMessage {
            from: 1,
            to: 2,
            sequence: 1,
        }]
    );
    assert!(ring.take_outbound(1).is_empty());

    ring.receive(2, first[0].clone());
    assert_eq!(ring.holder_ids(), vec![2]);

    ring.advance(2);
    let second = ring.take_outbound(2);
    assert_eq!(
        second,
        vec![TokenMessage {
            from: 2,
            to: 3,
            sequence: 2,
        }]
    );

    ring.receive(3, second[0].clone());
    assert_eq!(ring.holder_ids(), vec![3]);

    ring.advance(3);
    let third = ring.take_outbound(3);
    assert_eq!(
        third,
        vec![TokenMessage {
            from: 3,
            to: 1,
            sequence: 3,
        }]
    );

    ring.receive(1, third[0].clone());
    assert_eq!(ring.holder_ids(), vec![1]);
    assert_eq!(ring.passes(), 3);
}

#[test]
fn receive_rejects_unknown_misdirected_and_stale_messages() {
    let mut ring = Ring::new(3).expect("create three-node ring");

    ring.receive(
        2,
        TokenMessage {
            from: 99,
            to: 2,
            sequence: 10,
        },
    );
    assert_eq!(ring.holder_ids(), vec![1]);

    ring.receive(
        2,
        TokenMessage {
            from: 1,
            to: 3,
            sequence: 10,
        },
    );
    assert_eq!(ring.holder_ids(), vec![1]);

    ring.advance(99);
    assert_eq!(ring.holder_ids(), vec![1]);
    assert!(ring.take_outbound(99).is_empty());

    ring.advance(1);
    let message = ring
        .take_outbound(1)
        .into_iter()
        .next()
        .expect("node one emitted token");

    ring.receive(2, message.clone());
    assert_eq!(ring.holder_ids(), vec![2]);

    ring.receive(2, message);
    assert_eq!(ring.holder_ids(), vec![2]);
}

#[test]
fn node_ids_returns_independent_copy() {
    let ring = Ring::new(3).expect("create three-node ring");

    let mut ids = ring.node_ids();
    ids[0] = 99;

    assert_eq!(ring.node_ids(), vec![1, 2, 3]);
}
