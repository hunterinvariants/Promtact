// Licensed under the Apache License, Version 2.0.

use std::collections::BTreeMap;
use std::error::Error;
use std::fmt;

#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TokenMessage {
    pub from: u32,
    pub to: u32,
    pub sequence: u64,
}

#[derive(Clone, Debug, Default)]
struct Node {
    holding: bool,
    sequence: u64,
    outbound: Vec<TokenMessage>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RingError {
    TooFewNodes,
    TooManyNodes,
}

impl fmt::Display for RingError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::TooFewNodes => formatter.write_str("token ring requires at least two nodes"),
            Self::TooManyNodes => formatter.write_str("token ring exceeds the u32 node space"),
        }
    }
}

impl Error for RingError {}

#[derive(Clone, Debug)]
pub struct Ring {
    ids: Vec<u32>,
    nodes: BTreeMap<u32, Node>,
    passes: u64,
}

impl Ring {
    pub fn new(count: usize) -> Result<Self, RingError> {
        if count < 2 {
            return Err(RingError::TooFewNodes);
        }
        if count > u32::MAX as usize {
            return Err(RingError::TooManyNodes);
        }

        let mut ids = Vec::with_capacity(count);
        let mut nodes = BTreeMap::new();

        for index in 0..count {
            let id = u32::try_from(index + 1).map_err(|_| RingError::TooManyNodes)?;
            ids.push(id);
            nodes.insert(id, Node::default());
        }

        nodes
            .get_mut(&ids[0])
            .expect("first token-ring node exists")
            .holding = true;

        Ok(Self {
            ids,
            nodes,
            passes: 0,
        })
    }

    pub fn node_ids(&self) -> Vec<u32> {
        self.ids.clone()
    }

    pub fn advance(&mut self, id: u32) {
        let Some(successor) = self.successor(id) else {
            return;
        };
        let Some(current) = self.nodes.get_mut(&id) else {
            return;
        };
        if !current.holding {
            return;
        }

        current.holding = false;
        current.sequence = current.sequence.wrapping_add(1);

        current.outbound.push(TokenMessage {
            from: id,
            to: successor,
            sequence: current.sequence,
        });

        self.passes = self.passes.wrapping_add(1);
    }

    pub fn receive(&mut self, id: u32, message: TokenMessage) {
        if message.to != id || !self.nodes.contains_key(&message.from) {
            return;
        }

        let Some(current) = self.nodes.get_mut(&id) else {
            return;
        };
        if message.sequence <= current.sequence {
            return;
        }

        current.sequence = message.sequence;
        current.holding = true;
    }

    pub fn take_outbound(&mut self, id: u32) -> Vec<TokenMessage> {
        self.nodes
            .get_mut(&id)
            .map(|node| std::mem::take(&mut node.outbound))
            .unwrap_or_default()
    }

    pub fn holder_ids(&self) -> Vec<u32> {
        self.ids
            .iter()
            .copied()
            .filter(|id| self.nodes.get(id).is_some_and(|node| node.holding))
            .collect()
    }

    pub fn passes(&self) -> u64 {
        self.passes
    }

    fn successor(&self, id: u32) -> Option<u32> {
        self.ids
            .iter()
            .position(|candidate| *candidate == id)
            .map(|index| self.ids[(index + 1) % self.ids.len()])
    }
}
