import test from 'node:test';
import assert from 'node:assert/strict';
import { findChartHit, tooltipLeft, trafficPointLabel, averageHistoryRate } from './chartGeometry.js';

test('hit testing uses SVG screen matrix including letterbox translation', () => {
 const point = {at:'sample', hits:[{x:100,y:50}]};
 const matrix = {a:0.5,b:0,c:0,d:0.5,e:200,f:130};
 assert.equal(findChartHit([point],matrix,250,155)?.point,point);
 assert.equal(findChartHit([point],matrix,250,180),null);
});
test('hit testing handles affine transforms and bucket selection', () => {
 const points=[{x:10,y:20},{x:90,y:40}];
 const matrix={a:1,b:0.2,c:0.1,d:1,e:0,f:0};
 assert.equal(findChartHit(points,matrix,12,22)?.point,points[0]);
 assert.equal(findChartHit(points,matrix,90,1000,'bucket')?.point,points[1]);
 assert.equal(findChartHit(points,null,0,0),null);
});
test('tooltip stays within narrow and normal plot containers', () => {
 assert.equal(tooltipLeft(1,400,180),13);
 assert.equal(tooltipLeft(390,400,180),198);
 assert.equal(tooltipLeft(50,100,180),0);
});
test('half-hour and custom timestamps retain minute precision', () => {
 const value='2026-11-05T12:30:00Z';
 for(const range of ['1d','custom']) {
  const label=trafficPointLabel(value,range,'en-GB','UTC');
  assert.match(label,/12:30/);
 }
 assert.equal(trafficPointLabel('invalid','1d'),'invalid');
});
test('historical average uses exact selected window, never last active bucket', () => {
 const history={from:'2026-11-05T12:00:17Z',to:'2026-11-05T12:43:17Z',totalBytes:258000,latestSampleAt:'2026-11-05T12:02:00Z',bucketSeconds:60};
 assert.equal(averageHistoryRate(history),100);
 assert.equal(averageHistoryRate({...history,latestSampleAt:''}),null);
 assert.equal(averageHistoryRate({...history,to:history.from}),null);
 assert.equal(averageHistoryRate({...history,totalBytes:0}),0);
});
