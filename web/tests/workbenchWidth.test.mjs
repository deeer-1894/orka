import { createRequire } from 'node:module';
import { join } from 'node:path';
import { test } from 'node:test';
import assert from 'node:assert/strict';
const require=createRequire(import.meta.url);
const {widthBounds,clampWidth,keyboardWidth,readWidth,saveWidth}=require(join(process.env.ORKA_TEST_BUILD,'lib/workbenchWidth.js'));
test('workbench widths fit both desktop chat space and narrow windows',()=>{
 assert.deepEqual(widthBounds(1440),{min:360,max:720});assert.deepEqual(widthBounds(800),{min:360,max:480});assert.deepEqual(widthBounds(340),{min:324,max:324});assert.equal(clampWidth(900,1440),720);assert.equal(clampWidth(100,1440),360);assert.equal(clampWidth(NaN,1440),400);
});
test('separator keyboard direction expands to the left and stays bounded',()=>{
 assert.equal(keyboardWidth('ArrowLeft',400,1440),420);assert.equal(keyboardWidth('ArrowRight',400,1440,true),360);assert.equal(keyboardWidth('Home',600,1440),360);assert.equal(keyboardWidth('End',400,800),480);assert.equal(keyboardWidth('Enter',400,1440),null);
});
test('width preferences isolate owners and tolerate corrupt or unavailable storage',()=>{
 const map=new Map();const storage={getItem:k=>map.get(k)??null,setItem:(k,v)=>map.set(k,v)};
 saveWidth(storage,'owner-a',650);assert.equal(readWidth(storage,'owner-a'),650);assert.equal(readWidth(storage,'owner-b'),400);for(const key of map.keys())map.set(key,'NaN');assert.equal(readWidth(storage,'owner-a'),400);
 const denied={getItem:()=>{throw Error('denied');},setItem:()=>{throw Error('quota');}};assert.equal(readWidth(denied,'owner-a'),400);assert.doesNotThrow(()=>saveWidth(denied,'owner-a',500));
});
